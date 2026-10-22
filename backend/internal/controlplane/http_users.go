package controlplane

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,31}$`)

type userInput struct {
	PlanID   string `json:"plan_id"`
	Username string `json:"username"`
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`
	VLESS    bool   `json:"vless"`
	HY2      bool   `json:"hy2"`
	Expires  int64  `json:"expires"`
	Quota    int64  `json:"quota"`
}

func validUser(x userInput) bool {
	return usernamePattern.MatchString(x.Username) && x.Quota >= 0 && x.Quota <= 1<<60 && x.Expires >= 0 && x.Expires <= 253402300799
}
func (a *App) createUser(w http.ResponseWriter, r *http.Request, actor Record) {
	var input userInput
	if !decode(w, r, &input) {
		return
	}
	if !validUser(input) || len(input.Password) == 0 || len(input.Password) > 72 {
		failure(w, 400, "账号需为 3–32 位字母、数字或 ._-，密码需非空且不超过 72 个字节，额度与到期时间需有效")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		failure(w, 500, "创建密码失败")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var count int
	_ = a.store.db.QueryRow("SELECT COUNT(*) FROM users WHERE id NOT IN (SELECT user_id FROM archived_users)").Scan(&count)
	if count >= 100 {
		failure(w, 400, "个人版最多 100 位用户")
		return
	}
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	record := Record{User: User{Username: input.Username, Role: "user", Enabled: input.Enabled, Created: time.Now().Unix()}, Credentials: Credentials{HY2: randomToken(24), Token: randomToken(32), VLESS: map[string]string{}}, Password: hash}
	record.InitMeter("period-"+randomToken(12), record.Created)
	planID := input.PlanID
	// New members always receive a package.  Omitting one selects the safe,
	// zero-cost demo package; independent account configuration is removed.
	if planID == "" {
		planID = defaultDemoPlan
	}
	p, e := a.store.plan(planID)
	if e != nil && input.PlanID == "" {
		// Test stores and imported empty sites may not have run the bootstrap
		// seed yet.  Seed the catalog, then apply the same demo package.
		if seedErr := a.store.initDefaultPlans(); seedErr == nil {
			p, e = a.store.plan(planID)
		}
	}
	if e != nil || p.Archived {
		failure(w, 400, "套餐不存在或已归档")
		return
	}
	assignPlan(&record, p, time.Now().Unix())
	for _, n := range nodes {
		if n.Protocol == "vless" {
			record.Credentials.VLESS[n.ID] = uuid()
		}
	}
	if err = a.store.save(&record); err != nil {
		failure(w, 409, "账号已存在或保存失败")
		return
	}
	a.status = "pending"
	a.store.audit(actor.Username, "create_user", record.Username)
	jsonResponse(w, 201, record.User)
}
func (a *App) changeUser(w http.ResponseWriter, r *http.Request, actor Record) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/users/"), "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		failure(w, 400, "用户编号无效")
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if pending, e := a.store.commercePending(id); e != nil || pending {
		failure(w, 409, "请先处理用户的未完成订单")
		return
	}
	record, err := a.store.record(id)
	if err != nil {
		failure(w, 404, "用户不存在")
		return
	}
	if record.Mount != nil {
		if r.Method != "DELETE" || action != "" {
			failure(w, 409, "挂载身份仅用于代理；请在配对令牌中管理共享权限")
			return
		}
		record.Enabled = false
		record.Mount.LeaseUntil = 0
		record.Mount.NodeIDs = nil
		if err = a.store.save(&record); err != nil {
			failure(w, 500, "停用挂载身份失败")
			return
		}
		a.status = "pending"
		if err = a.reconcile(); err == nil {
			err = a.settleHYRevocations()
		}
		jsonResponse(w, 200, object{"ok": true, "pending": err != nil})
		return
	}
	if record.Archived {
		failure(w, 409, "账号已归档，历史记录只读")
		return
	}
	if r.Method == "DELETE" && action == "" {
		if record.Role == "owner" {
			failure(w, 400, "不能删除管理员")
			return
		}
		history, e := a.store.hasCommerceHistory(id)
		if e != nil {
			failure(w, 500, "账户记录检查失败")
			return
		}
		if history {
			wallet, e := a.store.wallet(id)
			if e != nil || wallet.Available != 0 || wallet.Held != 0 {
				failure(w, 409, "请先清算账户余额，再归档用户")
				return
			}
			if err = a.store.queueKick(id); err != nil {
				failure(w, 500, "撤销队列写入失败")
				return
			}
			record.Enabled = false
			record.Archived = true
			tx, e := a.store.db.Begin()
			if e == nil {
				e = txUser(tx, record.User)
				if e == nil {
					_, e = tx.Exec("INSERT INTO archived_users(user_id,created) VALUES(?,?)", id, time.Now().Unix())
				}
				if e == nil {
					_, e = tx.Exec("DELETE FROM sessions WHERE user_id=?", id)
				}
				if e == nil {
					e = tx.Commit()
				} else {
					tx.Rollback()
				}
			}
			if e != nil {
				failure(w, 500, "归档失败")
				return
			}
			a.status = "pending"
			jsonResponse(w, 200, object{"ok": true, "archived": true})
			return
		}
		if err = a.store.queueKick(id); err != nil {
			failure(w, 500, "撤销队列写入失败")
			return
		}
		_, err = a.store.db.Exec("DELETE FROM users WHERE id=?", id)
		if err != nil {
			failure(w, 500, "删除失败")
			return
		}
		a.status = "pending"
		a.store.audit(actor.Username, "delete_user", record.Username)
		jsonResponse(w, 200, object{"ok": true})
		return
	}
	if r.Method == "PUT" && action == "" {
		var input userInput
		if !decode(w, r, &input) {
			return
		}
		if !validUser(input) {
			failure(w, 400, "账号、额度或到期时间无效")
			return
		}
		if record.Role == "owner" && !input.Enabled {
			failure(w, 400, "不能停用管理员登录")
			return
		}
		if record.Entitlement != nil && (input.Quota != record.Quota || input.VLESS != record.VLESS || input.HY2 != record.HY2 || input.Expires != record.Expires) {
			failure(w, 409, "套餐权益请通过套餐与权益操作变更")
			return
		}
		if input.Password != "" {
			if len(input.Password) > 72 {
				failure(w, 400, "密码不能为空，且不能超过 72 个字节")
				return
			}
			record.Password, err = bcrypt.GenerateFromPassword([]byte(input.Password), 12)
			if err != nil {
				failure(w, 500, "密码更新失败")
				return
			}
			_, _ = a.store.db.Exec("DELETE FROM sessions WHERE user_id=?", id)
		}
		record.Username = input.Username
		record.Enabled = input.Enabled
		record.VLESS = input.VLESS
		record.HY2 = input.HY2
		record.Expires = input.Expires
		record.Quota = input.Quota
		record.Credentials.HYGeneration++
	} else if r.Method == "POST" {
		switch action {
		case "rotate-all-sub":
			record.Credentials.PublicToken = randomToken(32)
			record.Credentials.Token = randomToken(32)
		case "rotate-public-sub":
			record.Credentials.PublicToken = randomToken(32)
		case "rotate-sub":
			record.Credentials.Token = randomToken(32)
		case "revoke":
			record.Credentials.PublicToken = randomToken(32)
			record.Credentials.Token = randomToken(32)
			record.Credentials.HY2 = randomToken(24)
			for n := range record.Credentials.VLESS {
				record.Credentials.VLESS[n] = uuid()
			}
			if err = a.store.queueKick(id); err != nil {
				failure(w, 500, "撤销队列写入失败")
				return
			}
		case "reset-traffic":
			failure(w, 409, "请使用套餐权益中的开始新周期，生命周期用量不能清零")
			return
		default:
			failure(w, 404, "操作不存在")
			return
		}
	} else {
		failure(w, 405, "方法不支持")
		return
	}
	if err = a.validateBusinessUserQuota(record); err != nil {
		failure(w, 409, err.Error())
		return
	}
	if err = a.store.save(&record); err != nil {
		failure(w, 409, "账号重复或保存失败")
		return
	}
	a.status = "pending"
	a.store.audit(actor.Username, "update_user "+action, record.Username)
	jsonResponse(w, 200, record.User)
}
