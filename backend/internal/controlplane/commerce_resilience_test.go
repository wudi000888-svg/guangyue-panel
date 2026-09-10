package controlplane

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCommerceRecoveryAndRenewal(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	u := testUser(t, a, "member", "user")
	offer := commerceOffer(t, a, owner)
	fundWallet(t, a, owner, u, "5000")
	o := newOrder(t, a, u, offer)
	orderDo(t, a, u, o, "confirm")
	// An unavailable business grant holds provisioning; timeout releases funds.
	a.cfg.Edition = "pro"
	a.cfg.SiteID = "billing_controller"
	site, _ := createBusinessTest(t, a, owner, "billing_remote")
	site.Issued[u.ID] = 100
	site.SentGrants = []BusinessGrant{{UserID: u.ID, Quota: 100}}
	if e := a.store.saveBusinessSite(site); e != nil {
		t.Fatal(e)
	}
	now := time.Now().Unix()
	if e := a.commerceWork(now); e != nil {
		t.Fatal(e)
	}
	pending, _ := a.store.order(o.ID)
	if pending.State != "provisioning" || pending.NextAttempt <= now {
		t.Fatal("missing durable backoff")
	}
	// An application restart needs only persisted state to recover.
	restart := testApp(t)
	restart.store = a.store
	restart.cfg = a.cfg
	if e := restart.commerceWork(now + 1801); e != nil {
		t.Fatal(e)
	}
	w, _ := a.store.wallet(u.ID)
	if w.Available != 5000 || w.Held != 0 {
		t.Fatal("timeout lost funds")
	}
	site.Issued = map[int64]int64{}
	site.SentGrants = nil
	a.store.saveBusinessSite(site)
	o = newOrder(t, a, u, offer)
	orderDo(t, a, u, o, "confirm")
	if e := a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	before, _ := a.store.record(u.ID)
	before.Meter.Upload = 456
	if e := a.store.save(&before); e != nil {
		t.Fatal(e)
	}
	renewal := newOrder(t, a, u, offer)
	if renewal.Action != "renew" {
		t.Fatal("not renewal")
	}
	orderDo(t, a, u, renewal, "confirm")
	if e := a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	after, _ := a.store.record(u.ID)
	if after.Expires != before.Expires+30*86400 || after.QuotaUsed() != 456 || after.Meter.PeriodID != before.Meter.PeriodID {
		t.Fatal("renewal reset usage or period")
	}
	renewal, _ = a.store.order(renewal.ID)
	orderDo(t, a, owner, renewal, "refund")
	if e := a.commerceWork(time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	after, _ = a.store.record(u.ID)
	if after.Expires != before.Expires || after.QuotaUsed() != 456 {
		t.Fatal("refund did not preserve usage")
	}
	if e := a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}
func TestCommerceCaptureFailureIsAtomicAndDoesNotStarve(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	alice := testUser(t, a, "alice", "user")
	bob := testUser(t, a, "bob", "user")
	offer := commerceOffer(t, a, owner)
	fundWallet(t, a, owner, alice, "2000")
	fundWallet(t, a, owner, bob, "2000")
	first := newOrder(t, a, alice, offer)
	second := newOrder(t, a, bob, offer)
	orderDo(t, a, alice, first, "confirm")
	orderDo(t, a, bob, second, "confirm")
	_, e := a.store.db.Exec(fmt.Sprintf("CREATE TRIGGER fail_capture BEFORE INSERT ON money_transactions WHEN NEW.kind='purchase' AND NEW.user_id=%d BEGIN SELECT RAISE(ABORT,'fixture capture failure'); END", alice.ID))
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().Unix()
	if e = a.commerceWork(now); e == nil {
		t.Fatal("fault not injected")
	}
	v, _ := a.store.record(alice.ID)
	w, _ := a.store.wallet(alice.ID)
	if v.Entitlement != nil || w.Available != 1000 || w.Held != 1000 {
		t.Fatal("partial capture")
	}
	b, _ := a.store.order(second.ID)
	if b.State != "completed" {
		t.Fatal("second order starved")
	}
	a.store.db.Exec("DROP TRIGGER fail_capture")
	if e = a.commerceWork(now + 61); e != nil {
		t.Fatal(e)
	}
	if e = a.commerceWork(now + 62); e != nil {
		t.Fatal(e)
	}
	w, _ = a.store.wallet(alice.ID)
	if w.Available != 1000 || w.Held != 0 {
		t.Fatal("duplicate or missing capture")
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}
func TestCommerceIntegrityAndArchive(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	u := testUser(t, a, "member", "user")
	fundWallet(t, a, owner, u, "1000")
	if e := a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
	w := req(t, a, owner, "DELETE", fmt.Sprintf("/api/users/%d", u.ID), nil)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	var kicks int
	a.store.db.QueryRow("SELECT COUNT(*) FROM revocations WHERE user_id=?", u.ID).Scan(&kicks)
	if kicks != 0 {
		t.Fatal("rejected archive kicked user")
	}
	a.store.db.Exec("UPDATE wallet_accounts SET available=999 WHERE user_id=?", u.ID)
	if a.store.validateCommerce(false) == nil {
		t.Fatal("corrupt ledger accepted")
	}
	w = req(t, a, owner, "POST", "/api/commerce/adjust", object{"user_id": u.ID, "amount": "10", "kind": "gift", "reason": "fixture", "operation_id": randomToken(24), "password": commerceTestPassword})
	if w.Code != 409 {
		t.Fatal("corrupt write allowed")
	}
	a.store.db.Exec("UPDATE wallet_accounts SET available=1000 WHERE user_id=?", u.ID)
	w = req(t, a, owner, "POST", "/api/commerce/adjust", object{"user_id": u.ID, "amount": "1000", "kind": "debit", "reason": "settle fixture", "operation_id": randomToken(24), "password": commerceTestPassword})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = req(t, a, owner, "DELETE", fmt.Sprintf("/api/users/%d", u.ID), nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	archived, e := a.store.record(u.ID)
	if e != nil || !archived.Archived || archived.Enabled {
		t.Fatal("history user not archived")
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}
func TestSupportInternalImagesAndRetention(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	u := testUser(t, a, "member", "user")
	ticket := decoded[Ticket](t, req(t, a, u, "POST", "/api/support/tickets", object{"title": "A", "body": "Help", "category": "other", "operation_id": randomToken(24)}), 201)
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 10, 10)))
	// Store the normalized fixture using the same encrypted representation.
	encrypted, e := a.store.vault.seal(b.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	id := serial("GYA")
	_, e = a.store.db.Exec("INSERT INTO support_attachments(id,user_id,size,mime,created,expires,body) VALUES(?,?,?,'image/png',?,?,?)", id, owner.ID, b.Len(), time.Now().Unix(), time.Now().Unix()+900, encrypted)
	if e != nil {
		t.Fatal(e)
	}
	ticket = decoded[Ticket](t, req(t, a, owner, "POST", "/api/support/reply", object{"id": ticket.ID, "body": "private", "internal": true, "attachments": []string{id}, "operation_id": randomToken(24)}), 200)
	if w := req(t, a, u, "GET", "/api/support/attachment?id="+id, nil); w.Code != 404 {
		t.Fatal("private image exposed")
	}
	if w := req(t, a, owner, "GET", "/api/support/attachment?id="+id, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	ticket = decoded[Ticket](t, req(t, a, u, "POST", "/api/support/state", object{"id": ticket.ID, "revision": ticket.Revision, "state": "closed"}), 200)
	again := decoded[Ticket](t, req(t, a, u, "POST", "/api/support/state", object{"id": ticket.ID, "revision": ticket.Revision, "state": "closed"}), 200)
	if again.Closed != ticket.Closed || again.Revision != ticket.Revision {
		t.Fatal("duplicate close extended retention")
	}
	if e = a.supportCleanup(ticket.Closed + 91*86400); e != nil {
		t.Fatal(e)
	}
	if w := req(t, a, owner, "GET", "/api/support/attachment?id="+id, nil); w.Code != 404 {
		t.Fatal("expired image retained")
	}
	if e = a.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}
func TestSupportWorkerSubprocess(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 256, 256)))
	// Race instrumentation needs large shadow mappings incompatible with the
	// deployed worker's RLIMIT_DATA. Exercise the actual uninstrumented CLI;
	// the normalizer and all handlers are still covered by the race test suite.
	path := filepath.Join(t.TempDir(), "guangyue-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin/go"), "build", "-race=false", "-o", path, "../..")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build worker: %v: %s", e, out)
	}
	cmd := exec.Command(path, "-normalize-ticket-image", "png")
	cmd.Stdin = bytes.NewReader(b.Bytes())
	output, e := cmd.Output()
	if e != nil {
		t.Fatal("isolated worker", e)
	}
	if _, e = png.Decode(bytes.NewReader(output)); e != nil {
		t.Fatal(e)
	}
	// Failed uploads also consume the preflight resource budget.
	a := testApp(t)
	u := testUser(t, a, "member", "user")
	for i := 0; i < 30; i++ {
		if e = a.ticketUploadPreflight(u.ID); e != nil {
			t.Fatal(e)
		}
	}
	if e = a.ticketUploadPreflight(u.ID); e == nil {
		t.Fatal("upload attempt limit ignored")
	}
}

func TestCommerceSnapshotRoundTrip(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	u := testUser(t, a, "member", "user")
	codes := testCodes(t, a, owner, 1, time.Now().Unix()+86400)
	if w := req(t, a, u, "POST", "/api/commerce/redeem", object{"code": codes[0]["code"], "operation_id": randomToken(24)}); w.Code != 200 {
		t.Fatal(w.Code)
	}
	offer := commerceOffer(t, a, owner)
	o := newOrder(t, a, u, offer)
	orderDo(t, a, u, o, "confirm")
	dir := t.TempDir()
	if e := a.backupSnapshot(dir); e != nil {
		t.Fatal(e)
	}
	snapshot, e := openStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer snapshot.db.Close()
	if e = snapshot.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
	wallet, e := snapshot.wallet(u.ID)
	if e != nil || wallet.Available != 1500 || wallet.Held != 1000 {
		t.Fatal("snapshot financial mismatch")
	}
	dst := testApp(t)
	if e = persistence.Copy(context.Background(), snapshot.db, dst.store.db, false); e != nil {
		t.Fatal(e)
	}
	key, e := os.ReadFile(filepath.Join(dir, "master.key"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dst.cfg.StateDir, "master.key"), key, 0600); e != nil {
		t.Fatal(e)
	}
	dst.store.vault, e = openVault(dst.cfg.StateDir)
	if e != nil {
		t.Fatal(e)
	}
	if e = dst.store.validateCommerce(true); e != nil {
		t.Fatal(e)
	}
}

func TestSupportImageFormatsAndLimits(t *testing.T) {
	var b bytes.Buffer
	if e := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 12, 8)), nil); e != nil {
		t.Fatal(e)
	}
	// EXIF little-endian orientation 6 means a 90 degree clockwise rotation.
	exif := []byte{'E', 'x', 'i', 'f', 0, 0, 'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 1, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0, 0, 0, 0, 0}
	segment := []byte{255, 225, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(len(exif)+2))
	raw := append(append(append([]byte{}, b.Bytes()[:2]...), append(segment, exif...)...), b.Bytes()[2:]...)
	out, e := normalizeTicketImage(raw, "jpeg")
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := jpeg.DecodeConfig(bytes.NewReader(out))
	if e != nil || cfg.Width != 8 || cfg.Height != 12 || bytes.Contains(out, []byte("Exif")) {
		t.Fatal("JPEG metadata or orientation incorrect")
	}
	if _, e = normalizeTicketImage(raw, "png"); e == nil {
		t.Fatal("MIME mismatch accepted")
	}
	if _, e = normalizeTicketImage(make([]byte, (2<<20)+1), "png"); e == nil {
		t.Fatal("oversized image accepted")
	}
	b.Reset()
	png.Encode(&b, image.NewGray(image.Rect(0, 0, 4097, 1)))
	if _, e = normalizeTicketImage(b.Bytes(), "png"); e == nil {
		t.Fatal("oversized dimensions accepted")
	}
}
