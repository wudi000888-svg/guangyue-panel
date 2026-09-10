package controlplane

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"net/http"
	"sync"
	"testing"
	"time"
)

const commerceTestPassword = "test-password-123456"

func enableSales(t *testing.T, a *App) {
	t.Helper()
	_, e := a.store.db.Exec("INSERT INTO meta(key,value) VALUES('commerce_settings',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(jsonBytes(CommerceSettings{Sales: true, Redemption: true, Tickets: true, Currency: "CNY"})))
	if e != nil {
		t.Fatal(e)
	}
}
func fundWallet(t *testing.T, a *App, owner, user Record, amount string) {
	t.Helper()
	w := req(t, a, owner, "POST", "/api/commerce/adjust", object{"user_id": user.ID, "amount": amount, "kind": "credit", "reason": "test allocation", "operation_id": randomToken(24), "password": commerceTestPassword})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func testCodes(t *testing.T, a *App, owner Record, count int, expires int64) []object {
	t.Helper()
	w := req(t, a, owner, "POST", "/api/commerce/codes", object{"amount": "2500", "count": count, "expires": expires, "note": "fixture", "password": commerceTestPassword, "operation_id": randomToken(24)})
	v := decoded[struct {
		Codes []object `json:"codes"`
	}](t, w, 201)
	return v.Codes
}
func assertLedger(t *testing.T, a *App) {
	t.Helper()
	rows, e := a.store.db.Query("SELECT transaction_id,SUM(amount) FROM money_entries GROUP BY transaction_id HAVING SUM(amount)<>0")
	if e != nil {
		t.Fatal(e)
	}
	bad := rows.Next()
	rows.Close()
	if bad {
		t.Fatal("unbalanced ledger")
	}
	users, e := a.store.records()
	if e != nil {
		t.Fatal(e)
	}
	for _, u := range users {
		w, e := a.store.wallet(u.ID)
		if e != nil {
			t.Fatal(e)
		}
		for suffix, amount := range map[string]int64{"available": w.Available, "held": w.Held} {
			var sum int64
			e = a.store.db.QueryRow("SELECT COALESCE(SUM(amount),0) FROM money_entries WHERE account=?", "user:"+businessUsageKey(u.ID)+":"+suffix).Scan(&sum)
			if e != nil || sum != amount {
				t.Fatalf("wallet does not match entries: %v %d %d", e, sum, amount)
			}
		}
	}
}
func TestCommerceRedemptionAtomicAndRevocation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	alice := testUser(t, a, "alice", "user")
	bob := testUser(t, a, "bob", "user")
	codes := testCodes(t, a, owner, 4, time.Now().Unix()+1000)
	if w := req(t, a, alice, "GET", "/api/commerce/codes", nil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	in := object{"code": codes[0]["code"], "operation_id": randomToken(24)}
	w := req(t, a, alice, "POST", "/api/commerce/redeem", in)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = req(t, a, alice, "POST", "/api/commerce/redeem", in); w.Code != 200 {
		t.Fatal("replay", w.Code)
	}
	if w = req(t, a, bob, "POST", "/api/commerce/redeem", object{"code": codes[0]["code"], "operation_id": randomToken(24)}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	// Two controllers exercising the DB CAS, not only the shared application mutex.
	peer := testApp(t)
	peer.store = a.store
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i, u := range []Record{alice, bob} {
		app := a
		if i == 1 {
			app = peer
		}
		wg.Add(1)
		go func(app *App, u Record) {
			defer wg.Done()
			statuses <- req(t, app, u, "POST", "/api/commerce/redeem", object{"code": codes[1]["code"], "operation_id": randomToken(24)}).Code
		}(app, u)
	}
	wg.Wait()
	close(statuses)
	success := 0
	for code := range statuses {
		if code == 200 {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("redemptions=%d", success)
	}
	revoke := object{"ids": []any{codes[2]["id"]}, "password": commerceTestPassword, "operation_id": randomToken(24)}
	if w = req(t, a, owner, "POST", "/api/commerce/codes/revoke", revoke); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = req(t, a, alice, "POST", "/api/commerce/redeem", object{"code": codes[2]["code"], "operation_id": randomToken(24)}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	_, e := a.store.db.Exec("UPDATE redeem_codes SET expires=? WHERE id=?", time.Now().Unix()-1, codes[3]["id"])
	if e != nil {
		t.Fatal(e)
	}
	if w = req(t, a, alice, "POST", "/api/commerce/redeem", object{"code": codes[3]["code"], "operation_id": randomToken(24)}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	assertLedger(t, a)
	var total int64
	a.store.db.QueryRow("SELECT SUM(available) FROM wallet_accounts").Scan(&total)
	if total != 5000 {
		t.Fatal(total)
	}
}
func commerceOffer(t *testing.T, a *App, owner Record) Offer {
	t.Helper()
	enableSales(t, a)
	p := Plan{Name: "Office", Quota: 1 << 30, ValidDays: 30, Cycle: "30d", Timezone: "UTC", GroupIDs: []string{legacyPrivateGroup}, VLESS: true, HY2: true}
	w := req(t, a, owner, "POST", "/api/plans", p)
	p = decoded[Plan](t, w, 200)
	w = req(t, a, owner, "POST", "/api/commerce/offers", object{"plan_id": p.ID, "plan_version": p.Version, "price": "1000", "enabled": true})
	return decoded[Offer](t, w, 200)
}
func newOrder(t *testing.T, a *App, user Record, o Offer) Order {
	t.Helper()
	return decoded[Order](t, req(t, a, user, "POST", "/api/commerce/orders", object{"offer_id": o.ID, "offer_version": o.Version, "operation_id": randomToken(24)}), 201)
}
func orderDo(t *testing.T, a *App, actor Record, o Order, action string) Order {
	t.Helper()
	return decoded[Order](t, req(t, a, actor, "POST", "/api/commerce/orders/action", object{"id": o.ID, "action": action, "operation_id": randomToken(24), "password": commerceTestPassword}), 200)
}
func TestCommerceOrderCaptureResetAndRefund(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "member", "user")
	o := commerceOffer(t, a, owner)
	fundWallet(t, a, owner, user, "3000")
	u, _ := a.store.record(user.ID)
	u.Upload = 77
	u.Download = 100
	u.InitMeter("old", time.Now().Unix())
	if e := a.store.save(&u); e != nil {
		t.Fatal(e)
	}
	order := newOrder(t, a, user, o)
	orderDo(t, a, user, order, "confirm")
	wallet, _ := a.store.wallet(user.ID)
	if wallet.Available != 2000 || wallet.Held != 1000 {
		t.Fatal(wallet)
	}
	if e := a.advanceQuotaPeriods(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	u, _ = a.store.record(user.ID)
	if !u.Meter.PendingReset || u.Meter.PeriodID != "old" {
		t.Fatal("automatic reset crossed paid barrier")
	}
	if e := a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	order, _ = a.store.order(order.ID)
	if order.State != "completed" {
		t.Fatal(order)
	}
	u, _ = a.store.record(user.ID)
	if u.QuotaUsed() != 0 || u.Upload != 77 || u.Download != 100 {
		t.Fatal("lifetime counters lost")
	}
	wallet, _ = a.store.wallet(user.ID)
	if wallet.Available != 2000 || wallet.Held != 0 {
		t.Fatal(wallet)
	}
	if e := a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	wallet, _ = a.store.wallet(user.ID)
	if wallet.Available != 2000 {
		t.Fatal("duplicate capture")
	}
	orderDo(t, a, owner, order, "refund")
	if e := a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	wallet, _ = a.store.wallet(user.ID)
	if wallet.Available != 3000 || wallet.Held != 0 {
		t.Fatal(wallet)
	}
	order, _ = a.store.order(order.ID)
	if order.State != "refunded" {
		t.Fatal(order.State)
	}
	assertLedger(t, a)
}
func TestCommerceCancellationIsolationAndExpiry(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	bob := testUser(t, a, "bob", "user")
	o := commerceOffer(t, a, owner)
	fundWallet(t, a, owner, user, "1000")
	order := newOrder(t, a, user, o)
	if w := req(t, a, bob, "POST", "/api/commerce/orders/action", object{"id": order.ID, "action": "confirm", "operation_id": randomToken(24)}); w.Code != 404 {
		t.Fatal(w.Code)
	}
	orderDo(t, a, user, order, "confirm")
	orderDo(t, a, user, order, "cancel")
	if e := a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	v, _ := a.store.wallet(user.ID)
	if v.Available != 1000 || v.Held != 0 {
		t.Fatal(v)
	}
	u, _ := a.store.record(user.ID)
	if u.Entitlement != nil || u.Meter.PendingReset {
		t.Fatal("cancel issued entitlement")
	}
	order = newOrder(t, a, user, o)
	if e := a.commerceWork(order.Expires + 1); e != nil {
		t.Fatal(e)
	}
	expired, _ := a.store.order(order.ID)
	if expired.State != "expired" {
		t.Fatal(expired.State)
	}
	if w := req(t, a, user, "GET", "/api/commerce/wallet?user_id="+businessUsageKey(bob.ID), nil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	assertLedger(t, a)
}
func TestSupportTicketImagesAndOwnership(t *testing.T) {
	a := testApp(t)
	a.ticketProcessor = func(ctx context.Context, b []byte, f string) ([]byte, error) { return normalizeTicketImage(b, f) }
	owner := testUser(t, a, "owner", "owner")
	alice := testUser(t, a, "alice", "user")
	bob := testUser(t, a, "bob", "user")
	alice.Expires = time.Now().Unix() - 1
	a.store.save(&alice)
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 12, 10)))
	upload := func(u Record, data []byte, mime, name string) int {
		w := req(t, a, u, "POST", "/api/support/upload", object{"name": name, "mime": mime, "data": base64.StdEncoding.EncodeToString(data)})
		return w.Code
	}
	if code := upload(alice, []byte("<script>alert(1)</script>"), "image/png", "bad.png"); code != 400 {
		t.Fatal(code)
	}
	imageResult := decoded[struct {
		ID string `json:"id"`
	}](t, req(t, a, alice, "POST", "/api/support/upload", object{"name": "screen.png", "mime": "image/png", "data": base64.StdEncoding.EncodeToString(b.Bytes())}), 201)
	input := object{"title": "Connection issue", "category": "node", "body": "Please check this screenshot", "attachments": []string{imageResult.ID}, "operation_id": randomToken(24)}
	ticket := decoded[Ticket](t, req(t, a, alice, "POST", "/api/support/tickets", input), 201)
	for _, path := range []string{"/api/support/ticket?id=" + ticket.ID, "/api/support/attachment?id=" + imageResult.ID} {
		if w := req(t, a, bob, "GET", path, nil); w.Code != 404 {
			t.Fatal(w.Code)
		}
		if w := req(t, a, alice, "GET", path, nil); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if w := req(t, a, bob, "POST", "/api/support/tickets", object{"title": "Other", "category": "other", "body": "x", "attachments": []string{imageResult.ID}, "operation_id": randomToken(24)}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	decoded[Ticket](t, req(t, a, owner, "POST", "/api/support/reply", object{"id": ticket.ID, "body": "private note", "internal": true, "operation_id": randomToken(24)}), 200)
	response := req(t, a, alice, "GET", "/api/support/ticket?id="+ticket.ID, nil)
	if bytes.Contains(response.Body.Bytes(), []byte("private note")) {
		t.Fatal("internal note leaked")
	}
	if w := req(t, a, Record{}, "POST", "/api/support/tickets", input); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
	if e := a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := a.store.db.QueryRow("SELECT COUNT(*) FROM money_transactions").Scan(&n); e != nil || n != 0 {
		t.Fatal("ticket wrote money", e)
	}
}
func TestCommerceMoneyValidation(t *testing.T) {
	for _, v := range []string{"-1", "0", "1.5", "1e3", "01", "100000000001", "999999999999999999999"} {
		if _, e := moneyValue(v); e == nil {
			t.Fatal(v)
		}
	}
	a := testApp(t)
	u := testUser(t, a, "u", "user")
	w := req(t, a, u, "POST", "/api/commerce/adjust", object{"user_id": u.ID, "amount": "1", "kind": "credit", "reason": "forged", "operation_id": randomToken(24), "password": commerceTestPassword})
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
