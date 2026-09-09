package controlplane

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type runtimeResponse struct {
	RuntimeSettings
	Applied     bool `json:"applied"`
	Audit       bool `json:"audit_enabled"`
	History     bool `json:"traffic_history_enabled"`
	Access      bool `json:"subscription_access_enabled"`
	Application bool `json:"application_logs_enabled"`
	Cores       bool `json:"core_logs_enabled"`
}

func switchRuntimeTest(t *testing.T, a *App, owner Record, mode string) runtimeResponse {
	t.Helper()
	current := decoded[runtimeResponse](t, req(t, a, owner, "GET", "/api/runtime-settings", nil), 200)
	return decoded[runtimeResponse](t, req(t, a, owner, "PUT", "/api/runtime-settings", object{"mode": mode, "revision": current.Revision}), 200)
}
func runtimeTableCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRuntimeModePermissionsPersistenceAndRevision(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	path := "/api/runtime-settings"
	for _, method := range []string{"GET", "PUT"} {
		if req(t, a, member, method, path, object{}).Code != 403 || req(t, a, Record{}, method, path, object{}).Code != 401 {
			t.Fatal("runtime settings unauthorized access")
		}
	}
	initial := decoded[runtimeResponse](t, req(t, a, owner, "GET", path, nil), 200)
	if initial.Mode != runtimeNormal || !initial.Applied || !initial.Audit || !initial.History || !initial.Access || !initial.Application || !initial.Cores {
		t.Fatal("normal default is not fully active")
	}
	if req(t, a, owner, "PUT", path, object{"mode": "invalid", "revision": initial.Revision}).Code != 400 {
		t.Fatal("invalid mode accepted")
	}
	quiet := switchRuntimeTest(t, a, owner, runtimeNoLogs)
	if !quiet.Applied || quiet.Audit || quiet.History || quiet.Access || quiet.Application || quiet.Cores {
		t.Fatal("no_logs recording flags incorrect")
	}
	if req(t, a, owner, "PUT", path, object{"mode": runtimeNormal, "revision": initial.Revision}).Code != 409 {
		t.Fatal("stale runtime overwrite")
	}
	second, err := openStore(a.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.db.Close()
	if second.runtime.Mode != runtimeNoLogs || second.runtime.Revision != quiet.Revision || !second.runtimeFileMatchesLocked() {
		t.Fatal("mode did not survive reopening")
	}
	state := req(t, a, member, "GET", "/api/state", nil)
	if state.Code != 200 || strings.Contains(state.Body.String(), quiet.Revision) || !strings.Contains(state.Body.String(), `"traffic_history_enabled":false`) {
		t.Fatal("member runtime state leaks revision or misses recording mode")
	}
}

func TestRuntimeNoLogsPreservesAccountingAndExistingHistory(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	account := func(value int64) {
		t.Helper()
		if err := a.store.account([]Counter{{Key: "fixture-up", Generation: "one", Protocol: "vless", Direction: "up", UserID: owner.ID, Value: value}}); err != nil {
			t.Fatal(err)
		}
	}
	account(100)
	a.store.audit("owner", "fixture-before", "normal")
	u, _ := a.store.record(owner.ID)
	if err := a.store.markSubscriptionAccess(&u, 100); err != nil {
		t.Fatal(err)
	}
	// Retention cleanup must also pause while the mode explicitly preserves old records.
	oldHour := time.Now().Add(-40*24*time.Hour).Unix() / 3600 * 3600
	_, err := a.store.db.Exec("INSERT INTO traffic(hour,user_id,upload,download) VALUES(?,?,?,?)", oldHour, owner.ID, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	auditCount := runtimeTableCount(t, a.store, "audit")
	switchRuntimeTest(t, a, owner, runtimeNoLogs)
	a.store.audit("owner", "fixture-hidden", "should not persist")
	account(250)
	u, _ = a.store.record(owner.ID)
	if err = a.store.markSubscriptionAccess(&u, 999); err != nil {
		t.Fatal(err)
	}
	u, _ = a.store.record(owner.ID)
	var total, checkpoint int64
	a.store.db.QueryRow("SELECT SUM(upload) FROM traffic").Scan(&total)
	a.store.db.QueryRow("SELECT value FROM checkpoints WHERE key='fixture-up'").Scan(&checkpoint)
	if u.Upload != 250 || u.VLESSTraffic != 250 || checkpoint != 250 || u.LastSub != 100 || total != 120 || runtimeTableCount(t, a.store, "audit") != auditCount {
		t.Fatal("no_logs lost billing state, saved access, or changed history")
	}
	switchRuntimeTest(t, a, owner, runtimeNormal)
	account(300)
	u, _ = a.store.record(owner.ID)
	if err = a.store.markSubscriptionAccess(&u, 1200); err != nil {
		t.Fatal(err)
	}
	u, _ = a.store.record(owner.ID)
	a.store.db.QueryRow("SELECT SUM(upload) FROM traffic").Scan(&total)
	if u.Upload != 300 || u.LastSub != 1200 || total != 150 || runtimeTableCount(t, a.store, "audit") <= auditCount {
		t.Fatal("normal did not resume from live checkpoint without backfilling quiet history")
	}
}

func TestRuntimeNoLogsSubscriptionDeliveryDoesNotRecordAccess(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	owner.LastSub = 1234
	if err := a.store.save(&owner); err != nil {
		t.Fatal(err)
	}
	switchRuntimeTest(t, a, owner, runtimeNoLogs)
	path := "/sub/" + owner.Credentials.Token
	response := req(t, a, Record{}, "GET", path, nil)
	current, _ := a.store.record(owner.ID)
	if response.Code != 200 || response.Body.Len() == 0 || current.LastSub != 1234 {
		t.Fatal("quiet subscription delivery failed or recorded access")
	}
	switchRuntimeTest(t, a, owner, runtimeNormal)
	response = req(t, a, Record{}, "GET", path, nil)
	current, _ = a.store.record(owner.ID)
	if response.Code != 200 || current.LastSub <= 1234 {
		t.Fatal("normal subscription access recording did not resume")
	}
}

func TestRuntimeNoLogsCannotClaimAppliedWithoutInstalledWrappers(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	a.cfg.Dev = false
	t.Setenv("GUANGYUE_LOG_WRAPPER", "")
	state := decoded[runtimeResponse](t, req(t, a, owner, "GET", "/api/runtime-settings", nil), 200)
	if state.Applied {
		t.Fatal("unwrapped production service reported protected output")
	}
	if req(t, a, owner, "PUT", "/api/runtime-settings", object{"mode": runtimeNoLogs, "revision": state.Revision}).Code != 503 {
		t.Fatal("uninstalled wrappers accepted no_logs mode")
	}
	if a.store.runtime.Mode != runtimeNormal {
		t.Fatal("unready services changed persistent mode")
	}
}

func TestRuntimeModeFileFailureIsReportedAndFailClosed(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	initial := decoded[runtimeResponse](t, req(t, a, owner, "GET", "/api/runtime-settings", nil), 200)
	commit := filepath.Join(a.cfg.StateDir, runtimeCommitFile)
	if err := os.Remove(commit); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(commit, 0700); err != nil {
		t.Fatal(err)
	}
	w := req(t, a, owner, "PUT", "/api/runtime-settings", object{"mode": runtimeNoLogs, "revision": initial.Revision})
	if w.Code != 500 {
		t.Fatal("file application failure reported success")
	}
	failed := decoded[runtimeResponse](t, req(t, a, owner, "GET", "/api/runtime-settings", nil), 200)
	if failed.Applied || failed.Audit || failed.Application {
		t.Fatal("partial mode operation enabled recording")
	}
	var output bytes.Buffer
	writer := runtimeLogWriter{dir: a.cfg.StateDir, out: &output}
	_, _ = writer.Write([]byte("must stay quiet"))
	if output.Len() != 0 {
		t.Fatal("partial file commit emitted output")
	}
	if err := os.Remove(commit); err != nil {
		t.Fatal(err)
	}
	quiet := switchRuntimeTest(t, a, owner, runtimeNoLogs)
	if !quiet.Applied {
		t.Fatal("retry failed to recover commit marker")
	}
	// Failed DB writes during a no_logs -> normal attempt must never enable output.
	_, err := a.store.db.Exec("CREATE TRIGGER refuse_runtime BEFORE UPDATE ON meta WHEN OLD.key='runtime_settings' BEGIN SELECT RAISE(ABORT,'fixture'); END")
	if err != nil {
		t.Fatal(err)
	}
	if req(t, a, owner, "PUT", "/api/runtime-settings", object{"mode": runtimeNormal, "revision": quiet.Revision}).Code != 500 {
		t.Fatal("DB failure reported success")
	}
	_, _ = writer.Write([]byte("also stay quiet"))
	if output.Len() != 0 || a.store.runtime.Mode != runtimeNoLogs {
		t.Fatal("failed normal transition enabled output")
	}
}

func TestRuntimeLogWriterTransitionsAndMalformedFiles(t *testing.T) {
	a := testApp(t)
	var output bytes.Buffer
	w := runtimeLogWriter{dir: a.cfg.StateDir, out: &output}
	_, _ = w.Write([]byte("before"))
	if _, _, err := a.store.changeRuntimeMode(runtimeNoLogs, a.store.runtime.Revision); err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("hidden"))
	if _, _, err := a.store.changeRuntimeMode(runtimeNormal, a.store.runtime.Revision); err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("after"))
	if output.String() != "beforeafter" {
		t.Fatal("mode switch did not gate output without process restart")
	}
	for _, data := range []string{"", "normal", "invalid-revision unknown", strings.Repeat("x", 257), "another-valid-revision normal\n"} {
		if err := atomicWrite(filepath.Join(a.cfg.StateDir, runtimeModeFile), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("unexpected"))
	}
	if err := os.Remove(filepath.Join(a.cfg.StateDir, runtimeModeFile)); err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("missing"))
	if output.String() != "beforeafter" {
		t.Fatal("missing or invalid mode files did not fail closed")
	}
}

func TestRuntimeConcurrentQuietModeStopsNewAuditWrites(t *testing.T) {
	a := testApp(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				a.store.audit("fixture", "before-switch", "")
			}
		}()
	}
	if _, _, err := a.store.changeRuntimeMode(runtimeNoLogs, a.store.runtime.Revision); err != nil {
		t.Fatal(err)
	}
	count := runtimeTableCount(t, a.store, "audit")
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				a.store.audit("fixture", "after-switch", "")
			}
		}()
	}
	wg.Wait()
	if runtimeTableCount(t, a.store, "audit") != count {
		t.Fatal("concurrent audit writes crossed completed quiet switch")
	}
}

type runtimeSafeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *runtimeSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *runtimeSafeBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

func TestRuntimeWrapperChildFixture(t *testing.T) {
	if os.Getenv("GUANGYUE_RUNTIME_CHILD_FIXTURE") != "1" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	fmt.Fprintln(os.Stdout, "child ready")
	fmt.Fprintln(os.Stderr, "child diagnostic")
	<-ctx.Done()
	fmt.Fprintln(os.Stdout, "child received TERM")
	os.Exit(0)
}
func TestRuntimeWrapperForwardsSignalsAndHonorsLiveMode(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(fmt.Sprint(quiet), func(t *testing.T) {
			a := testApp(t)
			out, errOut := &runtimeSafeBuffer{}, &runtimeSafeBuffer{}
			cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeWrapperChildFixture$")
			cmd.Env = append(os.Environ(), "GUANGYUE_RUNTIME_CHILD_FIXTURE=1")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- superviseRuntimeChild(ctx, cmd, a.cfg.StateDir, out, errOut) }()
			deadline := time.Now().Add(5 * time.Second)
			for (!strings.Contains(out.String(), "child ready") || !strings.Contains(errOut.String(), "child diagnostic")) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if !strings.Contains(out.String(), "child ready") {
				t.Fatal("wrapper did not forward normal child output")
			}
			if quiet {
				if _, _, err := a.store.changeRuntimeMode(runtimeNoLogs, a.store.runtime.Revision); err != nil {
					t.Fatal(err)
				}
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("wrapper did not terminate child on SIGTERM")
			}
			if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
				t.Fatal("child did not exit after handling TERM")
			}
			if strings.Contains(out.String(), "child received TERM") == quiet {
				t.Fatal("live quiet state was ignored or normal output was lost")
			}
			if err := syscall.Kill(cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatal("wrapper left an orphan child")
			}
		})
	}
}

func TestRuntimeWrapperCommandWhitelist(t *testing.T) {
	for _, name := range []string{"panel", "xray", "hy2"} {
		path, args, err := runtimeServiceCommand(name)
		if err != nil || !strings.HasPrefix(path, "/opt/guangyue-personal/bin/") || len(args) == 0 {
			t.Fatal("fixed service command missing")
		}
	}
	for _, name := range []string{"sh", "/bin/sh", "xray -config /tmp/other", "../panel", ""} {
		if _, _, err := runtimeServiceCommand(name); err == nil {
			t.Fatal("arbitrary wrapper command accepted")
		}
	}
}
