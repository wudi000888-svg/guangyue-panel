package controlplane

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const moneyLimit int64 = 100000000000 // CNY 1 billion, integer cents.
var positiveMoney = regexp.MustCompile(`^[1-9][0-9]{0,11}$`)
var operationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)
var errCommerceConflict = errors.New("记录已变化，请刷新后重试")

type commerceError struct {
	status  int
	message string
}

func (e commerceError) Error() string               { return e.message }
func commerceFail(status int, message string) error { return commerceError{status, message} }
func commerceWriteError(w http.ResponseWriter, e error) {
	var ce commerceError
	if errors.As(e, &ce) {
		failure(w, ce.status, ce.message)
	} else {
		failure(w, 409, "操作未完成，请刷新状态后使用原操作标识重试")
	}
}
func moneyValue(s string) (int64, error) {
	if !positiveMoney.MatchString(s) {
		return 0, commerceFail(400, "金额必须是正整数分")
	}
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n > moneyLimit {
		return 0, commerceFail(400, "金额超出限制")
	}
	return n, nil
}
func moneyString(n int64) string { return strconv.FormatInt(n, 10) }
func serial(prefix string) string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return prefix + "-" + time.Now().UTC().Format("20060102150405.000000000") + "-" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}
func jsonBytes(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func validText(s string, max int) bool {
	return strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= max
}

type CommerceSettings struct {
	Sales      bool   `json:"sales"`
	Redemption bool   `json:"redemption"`
	Tickets    bool   `json:"tickets"`
	Currency   string `json:"currency"`
}

func (s *Store) commerceSettings() (CommerceSettings, error) {
	v := CommerceSettings{Redemption: true, Tickets: true, Currency: "CNY"}
	b, e := s.readMeta("commerce_settings")
	if e == nil && b != "" {
		e = json.Unmarshal([]byte(b), &v)
	}
	return v, e
}

type Wallet struct {
	UserID    int64  `json:"user_id"`
	Available int64  `json:"available,string"`
	Held      int64  `json:"held,string"`
	Currency  string `json:"currency"`
}

func (s *Store) wallet(user int64) (Wallet, error) {
	v := Wallet{UserID: user, Currency: "CNY"}
	e := s.db.QueryRow("SELECT available,held FROM wallet_accounts WHERE user_id=?", user).Scan(&v.Available, &v.Held)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return v, e
}
func (a *App) commerceActor(actor Record, admin bool, password string) (Record, error) {
	u, e := a.store.record(actor.ID)
	if e != nil || !u.Enabled {
		return u, commerceFail(401, "请登录")
	}
	if admin && (u.Role != "owner" || bcrypt.CompareHashAndPassword(u.Password, []byte(password)) != nil) {
		return u, commerceFail(403, "请验证管理员当前密码")
	}
	return u, nil
}
func (s *Store) commerceReplay(actor int64, id string, request any) ([]byte, error) {
	if !operationPattern.MatchString(id) {
		return nil, commerceFail(400, "缺少有效操作标识")
	}
	var owner int64
	var hash string
	var body []byte
	e := s.db.QueryRow("SELECT user_id,fingerprint,response FROM commerce_requests WHERE id=?", id).Scan(&owner, &hash, &body)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if owner != actor || hash != digest(string(jsonBytes(request))) {
		return nil, commerceFail(409, "操作标识已用于其他请求")
	}
	var result json.RawMessage
	e = s.vault.open(body, &result)
	return result, e
}
func (s *Store) saveCommerceRequest(tx *persistence.Tx, actor int64, id string, request, result any) error {
	b, e := s.vault.seal(result)
	if e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO commerce_requests(id,user_id,fingerprint,response,created) VALUES(?,?,?,?,?)", id, actor, digest(string(jsonBytes(request))), b, time.Now().Unix())
	return e
}
func txUser(tx *persistence.Tx, user domain.User) error {
	r, e := tx.Exec("UPDATE users SET doc=? WHERE id=?", jsonBytes(user), user.ID)
	if e == nil {
		n, _ := r.RowsAffected()
		if n != 1 {
			e = errCommerceConflict
		}
	}
	return e
}
func (s *Store) commercePending(user int64) (bool, error) {
	var n int
	e := s.db.QueryRow("SELECT COUNT(*) FROM commerce_orders WHERE user_id=? AND state IN ('pending','provisioning','refunding')", user).Scan(&n)
	return n > 0, e
}
func (s *Store) commerceBarrier(user int64) (bool, error) {
	var n int
	e := s.db.QueryRow("SELECT COUNT(*) FROM commerce_orders WHERE user_id=? AND state IN ('provisioning','refunding')", user).Scan(&n)
	return n > 0, e
}

// Every money movement posts balanced entries and updates the wallet in the same
// transaction. Conditional SQL protects correctness independently of App.mu.
func (s *Store) postMoney(tx *persistence.Tx, user, actor int64, event, kind, reference, reason string, availableDelta, heldDelta int64) (Wallet, error) {
	v := Wallet{UserID: user, Currency: "CNY"}
	if _, e := tx.Exec("INSERT INTO wallet_accounts(user_id) VALUES(?) ON CONFLICT(user_id) DO NOTHING", user); e != nil {
		return v, e
	}
	// Atomic update locks the wallet before reading its resulting balance.
	r, e := tx.Exec("UPDATE wallet_accounts SET available=available+?,held=held+?,revision=revision+1 WHERE user_id=? AND available+?>=0 AND available+?<=? AND held+?>=0 AND held+?<=? AND available+held+?+?<=?", availableDelta, heldDelta, user, availableDelta, availableDelta, moneyLimit, heldDelta, heldDelta, moneyLimit, availableDelta, heldDelta, moneyLimit)
	if e != nil {
		return v, e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return v, commerceFail(409, "可用余额不足或余额超出限制")
	}
	var revision int64
	if e = tx.QueryRow("SELECT available,held,revision FROM wallet_accounts WHERE user_id=?", user).Scan(&v.Available, &v.Held, &revision); e != nil {
		return v, e
	}
	// Verify the prior wallet checkpoint while its SQL row is locked.
	var beforeAvailable, beforeHeld int64
	if revision > 1 {
		if e = tx.QueryRow("SELECT available,held FROM money_transactions WHERE user_id=? AND wallet_revision=?", user, revision-1).Scan(&beforeAvailable, &beforeHeld); e != nil {
			return v, e
		}
	}
	if beforeAvailable != v.Available-availableDelta || beforeHeld != v.Held-heldDelta {
		return v, commerceFail(409, "资金账目校验失败，已阻止写入")
	}
	id := serial("GYB")
	amount := availableDelta + heldDelta
	if amount == 0 {
		amount = heldDelta
	}
	if _, e = tx.Exec("INSERT INTO money_transactions(id,event_key,user_id,actor_id,wallet_revision,kind,amount,available,held,reason,reference,created) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", id, event, user, actor, revision, kind, amount, v.Available, v.Held, reason, reference, time.Now().Unix()); e != nil {
		return v, e
	}
	entries := map[string]int64{fmt.Sprintf("user:%d:available", user): availableDelta, fmt.Sprintf("user:%d:held", user): heldDelta, "system:" + kind: -(availableDelta + heldDelta)}
	for account, amount := range entries {
		if amount == 0 {
			continue
		}
		if _, e = tx.Exec("INSERT INTO money_entries(transaction_id,account,amount) VALUES(?,?,?)", id, account, amount); e != nil {
			return v, e
		}
	}
	return v, nil
}
func (s *Store) commerceEvent(tx *persistence.Tx, user int64, id, body string) error {
	_, e := tx.Exec("INSERT INTO commerce_events(id,user_id,body,created) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING", id, user, body, time.Now().Unix())
	return e
}

func (s *Store) hasCommerceHistory(user int64) (bool, error) {
	for _, table := range []string{"money_transactions", "commerce_orders", "support_tickets", "support_replies"} {
		var n int
		if e := s.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE user_id=?", user).Scan(&n); e != nil {
			return false, e
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}
