package controlplane

import (
	"strings"
	"testing"
	"time"
)

func TestIntegrationsSnapshotPreservesMailAndPaymentRecovery(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	user := testUser(t, a, "alice", "user")
	settings := testEmailSettings(t, a, false)
	if _, err := a.issueEmailToken("registration", "person@example.test", 0, ""); err != nil {
		t.Fatal(err)
	}
	p := paymentTestMethod(t, a, owner, "alipay")
	attempt := paymentTestTopup(t, a, user, p, "1200")
	if err := a.recordPaymentEvent(p, paymentTestEvent(attempt, "snapshot-fixture-transaction"), []byte("snapshot-fixture")); err != nil {
		t.Fatal(err)
	}
	if err := a.paymentWork(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := a.backupSnapshot(dir); err != nil {
		t.Fatal(err)
	}
	snapshot, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.db.Close()
	if err := snapshot.validateCommerce(true); err != nil {
		t.Fatal(err)
	}
	recovered, err := snapshot.emailSettings()
	if err != nil || recovered.Password != settings.Password || recovered.Host != settings.Host {
		t.Fatal("SMTP encrypted config lost in recovery", err)
	}
	token := queuedEmailToken(t, &App{store: snapshot}, "registration")
	if _, err := snapshot.verifyEmailToken(token, time.Now().Unix()); err != nil {
		t.Fatal("queued verification lost in recovery", err)
	}
	provider, err := snapshot.paymentProvider(p.ID)
	if err != nil || provider.Config["secret"] != p.Config["secret"] {
		t.Fatal("merchant secret lost in recovery", err)
	}
	receipt := paymentTestReceipt(t, &App{store: snapshot}, "snapshot-fixture-transaction")
	if receipt.AttemptID != attempt.ID {
		t.Fatal("payment receipt lost")
	}
	wallet, err := snapshot.wallet(user.ID)
	if err != nil || wallet.Available != 1200 {
		t.Fatal("payment ledger lost", err)
	}
	peer := &App{store: snapshot}
	if err := peer.recordPaymentEvent(provider, paymentTestEvent(attempt, "snapshot-fixture-transaction"), []byte("snapshot-replay-fixture")); err != nil {
		t.Fatal(err)
	}
	if err := peer.paymentWork(); err != nil {
		t.Fatal(err)
	}
	wallet, _ = snapshot.wallet(user.ID)
	if wallet.Available != 1200 {
		t.Fatal("replayed payment duplicated after restore")
	}
}

func TestIntegrationsRestoreRejectsDamagedMailCiphertext(t *testing.T) {
	for _, table := range []string{"email_settings", "email_outbox"} {
		t.Run(table, func(t *testing.T) {
			a := testApp(t)
			testEmailSettings(t, a, false)
			if _, err := a.issueEmailToken("registration", "person@example.test", 0, ""); err != nil {
				t.Fatal(err)
			}
			field := "doc"
			if strings.HasSuffix(table, "outbox") {
				field = "body"
			}
			if _, err := a.store.db.Exec("UPDATE "+table+" SET "+field+"=?", []byte("damaged-encrypted-data")); err != nil {
				t.Fatal(err)
			}
			if err := a.store.validateCommerce(true); err == nil {
				t.Fatal("damaged email ciphertext accepted by restore validation")
			}
		})
	}
}
