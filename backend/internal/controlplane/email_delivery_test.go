package controlplane

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmailOutboxPersistsRetriesAndDeliversThroughTLS(t *testing.T) {
	a := testApp(t)
	fixture := newSMTPFixture(t, "starttls", true, false)
	settings, err := a.store.saveEmailSettings(fixture.settings)
	if err != nil {
		t.Fatal(err)
	}
	owner := testUser(t, a, "owner", "owner")
	response := decoded[object](t, req(t, a, owner, "POST", "/api/email/test", object{"to": "receiver@example.test", "admin_password": "test-password-123456"}), 202)
	id := response["delivery_id"].(string)
	if len(fixture.messages) != 0 {
		t.Fatal("HTTP handler sent synchronously")
	}
	now := time.Now().Unix()
	found, err := a.store.processEmail(context.Background(), now, func(context.Context, EmailSettings, emailMessage) error { return errors.New("transient mail failure") })
	if err != nil || !found {
		t.Fatal(err)
	}
	deliveries, err := a.store.emailDeliveries()
	if err != nil || len(deliveries) != 1 || deliveries[0].State != "retry" || deliveries[0].Attempts != 1 || deliveries[0].NextAttempt <= now {
		t.Fatal("outbox did not persist retry state", deliveries, err)
	}
	// A fresh store object sees the durable queue; no in-memory send state is required.
	recovered := &Store{db: a.store.db, vault: a.store.vault}
	if found, err = recovered.processEmail(context.Background(), now, func(context.Context, EmailSettings, emailMessage) error {
		t.Fatal("retried before backoff")
		return nil
	}); err != nil || found {
		t.Fatal("premature queue retry", err)
	}
	if found, err = recovered.processEmail(context.Background(), now+31, func(ctx context.Context, v EmailSettings, m emailMessage) error {
		return sendSMTP(ctx, v, m, &tls.Config{RootCAs: fixture.roots})
	}); err != nil || !found {
		t.Fatal(err)
	}
	deliveries, err = recovered.emailDeliveries()
	if err != nil || deliveries[0].State != "sent" || deliveries[0].Attempts != 2 || deliveries[0].SentAt == 0 || len(fixture.messages) != 1 {
		t.Fatal("queued SMTP delivery did not complete", deliveries, err)
	}
	if a.store.retryEmail(id, now) == nil {
		t.Fatal("already sent mail can be replayed by retry")
	}
	settings.Enabled = false
	settings.RegistrationVerification = false
	if _, err = a.store.saveEmailSettings(settings); err != nil {
		t.Fatal(err)
	}
	_, err = a.store.queueEmail(0, "receiver@example.test", "test", "Test", "Paused", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if found, err = a.store.processEmail(context.Background(), now+60, func(context.Context, EmailSettings, emailMessage) error {
		t.Fatal("disabled SMTP sent mail")
		return nil
	}); err != nil || found {
		t.Fatal(err)
	}
}
func TestEmailOutboxLeaseAndBoundedRetries(t *testing.T) {
	a := testApp(t)
	testEmailSettings(t, a, false)
	id, err := a.store.queueEmail(0, "receiver@example.test", "test", "Test", "Test", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	now := time.Now().Unix()
	go func() {
		_, err := a.store.processEmail(context.Background(), now, func(context.Context, EmailSettings, emailMessage) error { close(started); <-release; return nil })
		done <- err
	}()
	<-started
	var sends atomic.Int32
	if found, err := a.store.processEmail(context.Background(), now, func(context.Context, EmailSettings, emailMessage) error { sends.Add(1); return nil }); err != nil || found || sends.Load() != 0 {
		t.Fatal("active delivery lease duplicated", err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("UPDATE email_outbox SET state='sending',attempts=1,sent_at=0,lease_until=?,lease_token='crashed' WHERE id=?", now-1, id); err != nil {
		t.Fatal(err)
	}
	for attempt := 2; attempt <= 5; attempt++ {
		found, err := a.store.processEmail(context.Background(), now+int64(attempt)*1000, func(context.Context, EmailSettings, emailMessage) error { return errors.New("offline") })
		if err != nil || !found {
			t.Fatal("expired lease not recovered", attempt, err)
		}
	}
	deliveries, err := a.store.emailDeliveries()
	if err != nil || deliveries[0].State != "failed" || deliveries[0].Attempts != 5 {
		t.Fatal("retry limit not enforced", deliveries, err)
	}
	if err = a.store.retryEmail(id, now); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.processEmail(context.Background(), now, func(context.Context, EmailSettings, emailMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	expired, err := a.store.queueEmail(0, "receiver@example.test", "bind", "Verify", "Expired token", "", now-1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.processEmail(context.Background(), now, func(context.Context, EmailSettings, emailMessage) error {
		t.Fatal("expired verification link sent")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if a.store.retryEmail(expired, now) == nil {
		t.Fatal("expired mail accepted for retry")
	}
}
func TestEmailCommerceEventCaptureAtomicAndIdempotent(t *testing.T) {
	a := testApp(t)
	settings := testEmailSettings(t, a, false)
	settings.EventsAfter = 0
	user := testUser(t, a, "alice", "user")
	now := time.Now().Unix()
	if _, err := a.store.db.Exec("INSERT INTO email_accounts(user_id,email,verified_at) VALUES(?,?,?)", user.ID, "alice@example.test", now-1); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"order:fixture:completed", "order:fixture:refunded"} {
		if _, err := a.store.db.Exec("INSERT INTO commerce_events(id,user_id,body,created) VALUES(?,?,?,?)", id, user.ID, "Order <unsafe> updated", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.store.db.Exec("ALTER TABLE email_outbox RENAME TO email_outbox_unavailable"); err != nil {
		t.Fatal(err)
	}
	if a.store.captureEmailEvents(settings, now) == nil {
		t.Fatal("missing outbox did not fail capture")
	}
	var receipts int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM email_event_receipts").Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("failed outbox insertion swallowed an event")
	}
	if _, err := a.store.db.Exec("ALTER TABLE email_outbox_unavailable RENAME TO email_outbox"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := a.store.captureEmailEvents(settings, now); err != nil {
			t.Fatal(err)
		}
	}
	deliveries, err := a.store.emailDeliveries()
	if err != nil || len(deliveries) != 2 {
		t.Fatal("commerce mail missing or duplicated", deliveries, err)
	}
	var notified int
	if err = a.store.db.QueryRow("SELECT SUM(notified) FROM commerce_events").Scan(&notified); err != nil || notified != 0 {
		t.Fatal("mail capture consumed in-app notification state")
	}
	if err = a.store.db.QueryRow("SELECT COUNT(*) FROM email_event_receipts WHERE state='queued'").Scan(&receipts); err != nil || receipts != 2 {
		t.Fatal("mail event receipt missing")
	}
}
func TestEmailConcurrentConfigurationDoesNotOverwrite(t *testing.T) {
	a := testApp(t)
	original := testEmailSettings(t, a, false)
	var wg sync.WaitGroup
	var successes atomic.Int32
	for _, name := range []string{"one", "two"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			v := original
			v.FromName = name
			if _, err := a.store.saveEmailSettings(v); err == nil {
				successes.Add(1)
			}
		}(name)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("SMTP CAS did not reject concurrent edit", successes.Load())
	}
}

func TestEmailOrderNotificationRechecksRecipient(t *testing.T) {
	a := testApp(t)
	testEmailSettings(t, a, false)
	user := testUser(t, a, "alice", "user")
	now := time.Now().Unix()
	if _, err := a.store.db.Exec("INSERT INTO email_accounts(user_id,email,verified_at) VALUES(?,?,?)", user.ID, "new@example.test", now); err != nil {
		t.Fatal(err)
	}
	id, err := a.store.queueEmail(user.ID, "old@example.test", "order", "Order", "Private order details", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	found, err := a.store.processEmail(context.Background(), time.Now().Unix(), func(context.Context, EmailSettings, emailMessage) error {
		t.Fatal("order details sent to a replaced email address")
		return nil
	})
	if err != nil || !found {
		t.Fatal("stale recipient was not processed", err)
	}
	items, err := a.store.emailDeliveries()
	if err != nil || len(items) != 1 || items[0].State != "cancelled" {
		t.Fatal("stale recipient delivery was not cancelled", items, err)
	}
	if a.store.retryEmail(id, now) == nil {
		t.Fatal("cancelled private notification can be retried")
	}
}
