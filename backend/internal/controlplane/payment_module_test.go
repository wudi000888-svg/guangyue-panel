package controlplane

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func paymentModuleSettings(t *testing.T, a *App, owner Record, enabled bool, password string, status int) {
	t.Helper()
	w := req(t, a, owner, "POST", "/api/commerce/settings", object{"settings": object{"currency": "CNY", "mail_provider": "none", "redemption": true, "tickets": true, "payment_module_enabled": enabled}, "password": password})
	if w.Code != status {
		t.Fatalf("module settings: %d want %d: %s", w.Code, status, w.Body.String())
	}
}

func TestPaymentModuleTakeoverDefaultsAndLocalOverride(t *testing.T) {
	a := testApp(t)
	a.cfg.Edition = "pro" // Lite intentionally issues management tokens only.
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	assert := func(want bool) {
		t.Helper()
		settings := decoded[CommerceSettings](t, req(t, a, member, "GET", "/api/commerce/settings", nil), 200)
		if settings.PaymentModuleEnabled == nil || *settings.PaymentModuleEnabled != want {
			t.Fatal("unexpected payment module policy", settings.PaymentModuleEnabled)
		}
	}
	assert(true)
	paymentModuleSettings(t, a, owner, true, commerceTestPassword, 200)
	gateway := func(scope string) {
		t.Helper()
		token := fleetToken(t, a, owner, scope)
		r := httptest.NewRequest("POST", "/api/fleet-gateway", strings.NewReader(string(jsonBytes(object{"method": "GET", "path": "/api/operations", "body": object{}}))))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Requested-With", "guangyue")
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("gateway %s: %d %s", scope, w.Code, w.Body.String())
		}
	}
	gateway("read")
	assert(true)
	gateway("manage")
	assert(false)
	paymentModuleSettings(t, a, member, true, commerceTestPassword, 403)
	paymentModuleSettings(t, a, owner, true, "wrong-password", 403)
	assert(false)
	paymentModuleSettings(t, a, owner, true, commerceTestPassword, 200)
	gateway("manage") // Reissued credentials and repeated controller polls.
	assert(true)
	b := cryptoIndependentApp(t, a)
	if on, e := b.paymentModuleEnabled(); e != nil || !on {
		t.Fatal("override lost across reopen", on, e)
	}
	a.cfg.Role, a.cfg.LocalManagement = "business", false
	paymentModuleSettings(t, a, owner, true, commerceTestPassword, 409)
	if on, e := a.paymentModuleEnabled(); e != nil || on {
		t.Fatal("pull-only business agent opened payments", on, e)
	}
	a.cfg.LocalManagement = true
	paymentModuleSettings(t, a, owner, true, commerceTestPassword, 200)
	assert(true)
}

func TestPaymentModuleKeepsApprovedSweep(t *testing.T) {
	a, owner, wallet, rpc := cryptoSweepFixture(t)
	preview := cryptoSweepTestPreview(t, a, owner, wallet)
	job := cryptoSweepTestCreate(t, a, owner, wallet, preview)
	if e := a.store.defaultTakenOverPayments(); e != nil {
		t.Fatal(e)
	}
	for range 4 {
		if e := a.cryptoSweepWork(context.Background()); e != nil {
			t.Fatal(e)
		}
		rpc.mine(true)
	}
	current, e := a.store.cryptoSweepJob(job.ID)
	if e != nil || current.State != "complete" || cryptoSweepTestCount(t, a) != 2 {
		t.Fatal("approved sweep stopped or repeated", current, e)
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}

func TestPaymentModuleBlocksIntentsButSettlesExistingFunds(t *testing.T) {
	a, owner, member, wallet, rpc := cryptoPaymentFixture(t)
	order, invoice := cryptoPaymentInvoice(t, a, owner, member)
	if e := a.store.defaultTakenOverPayments(); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"orders", "orders/pay", "wallet/topup", "redeem", "crypto/invoices", "crypto/admin/wallets/" + wallet.ID + "/sweeps/preview"} {
		actor := member
		if strings.Contains(path, "admin/") {
			actor = owner
		}
		if w := req(t, a, actor, "POST", "/api/commerce/"+path, object{}); w.Code != 403 {
			t.Fatalf("disabled module allowed %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"payment-methods", "crypto/admin/settings"} {
		if w := req(t, a, owner, "POST", "/api/commerce/"+path, object{"enabled": true, "password": commerceTestPassword}); w.Code != 403 {
			t.Fatalf("disabled module enabled %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := req(t, a, member, "GET", "/api/commerce/crypto/invoices/"+invoice.ID, nil); w.Code != 200 {
		t.Fatal("history hidden", w.Code)
	}
	options := decoded[struct {
		Enabled bool `json:"enabled"`
	}](t, req(t, a, member, "GET", "/api/commerce/crypto/options", nil), 200)
	if options.Enabled {
		t.Fatal("disabled module advertises new crypto payments")
	}
	rpc.add(invoice, 101, invoice.ExpectedAtoms)
	rpc.latest, rpc.final = 102, 102
	stored, e := a.store.cryptoInvoice(a.store.db, invoice.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.scanCryptoInvoice(context.Background(), stored); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = a.paymentWork(); e != nil {
			t.Fatal(e)
		}
		if e = a.commerceWork(time.Now().Unix()); e != nil {
			t.Fatal(e)
		}
	}
	completed, e := a.store.order(order.ID)
	if e != nil || completed.State != "completed" {
		t.Fatal("existing funds stranded by module switch", completed.State, e)
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}
