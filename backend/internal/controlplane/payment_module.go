package controlplane

import (
	"errors"
	"net/http"
	"strings"
)

// Local administrators may enable payments explicitly after token takeover.
// The takeover default is applied once, never on every controller poll.
func (a *App) paymentModuleEnabled() (bool, error) {
	v, e := a.store.readMeta("payment_module_enabled")
	if e != nil {
		return false, e
	}
	switch v {
	case "true":
		return !a.cfg.businessAgent(), nil
	case "false":
		return false, nil
	case "":
		return a.cfg.Role != "business", nil
	default:
		return false, errors.New("invalid payment module policy")
	}
}

func (s *Store) defaultTakenOverPayments() error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.Exec("INSERT INTO meta(key,value) VALUES('payment_takeover_initialized','true') ON CONFLICT(key) DO NOTHING")
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n == 1 {
		if _, e = tx.Exec("INSERT INTO meta(key,value) VALUES('payment_module_enabled','false') ON CONFLICT(key) DO UPDATE SET value='false'"); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (a *App) requirePaymentModule() error {
	enabled, e := a.paymentModuleEnabled()
	if e != nil {
		return e
	}
	if !enabled {
		return commerceFail(403, "本站支付模块已关闭，请联系本站管理员")
	}
	return nil
}

// Stop new payment intents, while preserving historical reads, callbacks,
// backup/export and cancellation. Existing verified funds must still settle.
func (a *App) paymentModuleGate(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != "POST" {
		return false
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/commerce/")
	blocked := path == "orders" || path == "orders/pay" || path == "wallet/topup" || path == "redeem" || path == "crypto/invoices"
	if strings.HasPrefix(path, "crypto/admin/wallets/") {
		blocked = blocked || strings.HasSuffix(path, "/sweeps") || strings.HasSuffix(path, "/sweeps/preview")
	}
	if strings.HasPrefix(path, "crypto/admin/sweeps/") && strings.HasSuffix(path, "/retry") {
		blocked = true
	}
	if !blocked {
		return false
	}
	if e := a.requirePaymentModule(); e != nil {
		commerceWriteError(w, e)
		return true
	}
	return false
}
