package controlplane

import (
	"database/sql"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// registrationChallenge is deliberately small and in-memory. It is a local
// friction check for public sign-up; the shape leaves room for a hosted CAPTCHA
// provider later without coupling registration to an external service today.
type registrationChallenge struct {
	target  int
	expires time.Time
}

func (a *App) registrationChallenge(w http.ResponseWriter, r *http.Request) {
	if !a.store.siteSettings().RegistrationEnabled {
		failure(w, 404, "注册功能未开放")
		return
	}
	token := randomToken(24)
	target := 56 + int(time.Now().UnixNano()%150)
	a.registrationMu.Lock()
	if a.registrationChallenges == nil {
		a.registrationChallenges = map[string]registrationChallenge{}
	}
	for key, challenge := range a.registrationChallenges {
		if time.Now().After(challenge.expires) {
			delete(a.registrationChallenges, key)
		}
	}
	a.registrationChallenges[token] = registrationChallenge{target: target, expires: time.Now().Add(5 * time.Minute)}
	a.registrationMu.Unlock()
	jsonResponse(w, 200, object{"token": token, "width": 240, "target": target, "tolerance": 14, "expires": time.Now().Add(5 * time.Minute).Unix()})
}

func requestIP(r *http.Request) string {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "127.0.0.1" || ip == "::1" {
		if x := r.Header.Get("X-Real-IP"); net.ParseIP(x) != nil {
			ip = x
		}
	}
	return ip
}

func (a *App) register(w http.ResponseWriter, r *http.Request) {
	settings := a.store.siteSettings()
	if !settings.RegistrationEnabled {
		failure(w, 403, "注册功能未开放")
		return
	}
	if !a.rateAllowed("register:" + requestIP(r)) {
		failure(w, 429, "注册尝试过多，请稍后重试")
		return
	}
	var input struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		ConfirmPassword string `json:"confirm_password"`
		CaptchaToken    string `json:"captcha_token"`
		CaptchaPosition int    `json:"captcha_position"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !usernamePattern.MatchString(input.Username) || len(input.Password) < 8 || len(input.Password) > 72 || input.Password != input.ConfirmPassword {
		failure(w, 400, "账号需为 3–32 位字母、数字或 ._-，密码需为 8–72 个字节且两次输入一致")
		return
	}
	if settings.RegistrationCaptcha {
		a.registrationMu.Lock()
		challenge, ok := a.registrationChallenges[input.CaptchaToken]
		if ok && time.Now().After(challenge.expires) {
			delete(a.registrationChallenges, input.CaptchaToken)
			ok = false
		}
		if ok && abs(input.CaptchaPosition-challenge.target) > 14 {
			ok = false
		}
		if ok {
			delete(a.registrationChallenges, input.CaptchaToken)
		}
		a.registrationMu.Unlock()
		if !ok {
			failure(w, 400, "请完成滑动验证")
			return
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		failure(w, 500, "创建密码失败")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var count int
	if err = a.store.db.QueryRow("SELECT COUNT(*) FROM users WHERE id NOT IN (SELECT user_id FROM archived_users)").Scan(&count); err != nil || count >= 100 {
		failure(w, 400, "当前站点暂时无法接受更多注册")
		return
	}
	var existing int64
	if err = a.store.db.QueryRow("SELECT id FROM users WHERE username=?", input.Username).Scan(&existing); err == nil {
		failure(w, 409, "账号已存在")
		return
	} else if err != sql.ErrNoRows {
		failure(w, 500, "检查账号失败")
		return
	}
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	now := time.Now().Unix()
	record := Record{User: User{Username: input.Username, Role: "user", Enabled: true, Created: now}, Credentials: Credentials{HY2: randomToken(24), Token: randomToken(32), VLESS: map[string]string{}}, Password: hash}
	record.InitMeter("period-"+randomToken(12), now)
	plan, err := a.store.plan(defaultDemoPlan)
	if err != nil {
		if seedErr := a.store.initDefaultPlans(); seedErr == nil {
			plan, err = a.store.plan(defaultDemoPlan)
		}
	}
	if err != nil || plan.Archived {
		failure(w, 409, "默认演示套餐不可用，请联系管理员")
		return
	}
	assignPlan(&record, plan, now)
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
	a.store.audit("system", "register", record.Username)
	token := randomToken(32)
	expires := time.Now().Add(8 * time.Hour)
	if _, err = a.store.db.Exec("INSERT INTO sessions(token_hash,user_id,expires) VALUES(?,?,?)", digest(token), record.ID, expires.Unix()); err != nil {
		failure(w, 500, "无法创建会话")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "gy_session", Value: token, Path: "/", HttpOnly: true, Secure: !a.cfg.Dev, SameSite: http.SameSiteStrictMode, Expires: expires})
	jsonResponse(w, 201, object{"user": record.User})
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
