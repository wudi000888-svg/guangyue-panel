package controlplane

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const runtimeNormal = "normal"
const runtimeNoLogs = "no_logs"
const runtimeModeFile = "runtime-mode"
const runtimeCommitFile = "runtime-mode.commit"

type RuntimeSettings struct {
	Mode      string `json:"mode"`
	Revision  string `json:"revision"`
	ChangedAt int64  `json:"changed_at"`
}

func validRuntimeSettings(v RuntimeSettings) bool {
	return (v.Mode == runtimeNormal || v.Mode == runtimeNoLogs) && len(v.Revision) >= 12 && len(v.Revision) <= 64 && !strings.ContainsAny(v.Revision, " \t\r\n")
}

// A commit marker prevents a partially applied normal-mode change from enabling
// output. Both files must agree; missing, unreadable and malformed state is quiet.
func runtimeFileState(dir string) (RuntimeSettings, bool) {
	read := func(name string) ([]byte, error) {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(io.LimitReader(f, 257))
	}
	mode, err := read(runtimeModeFile)
	if err != nil || len(mode) > 256 {
		return RuntimeSettings{}, false
	}
	parts := strings.Fields(string(mode))
	if len(parts) != 2 {
		return RuntimeSettings{}, false
	}
	v := RuntimeSettings{Revision: parts[0], Mode: parts[1]}
	if !validRuntimeSettings(v) {
		return RuntimeSettings{}, false
	}
	commit, err := read(runtimeCommitFile)
	return v, err == nil && string(commit) == v.Revision+"\n"
}

func writeRuntimeMode(dir string, v RuntimeSettings) error {
	return atomicWrite(filepath.Join(dir, runtimeModeFile), []byte(v.Revision+" "+v.Mode+"\n"), 0600)
}
func commitRuntimeMode(dir string, v RuntimeSettings) error {
	return atomicWrite(filepath.Join(dir, runtimeCommitFile), []byte(v.Revision+"\n"), 0600)
}

func (s *Store) initRuntimeSettings() error {
	value, err := s.readMeta("runtime_settings")
	if err != nil {
		return err
	}
	v := RuntimeSettings{Mode: runtimeNormal, Revision: randomToken(12)}
	if value == "" {
		b, _ := json.Marshal(v)
		if err := s.setMeta("runtime_settings", string(b)); err != nil {
			return err
		}
	} else if json.Unmarshal([]byte(value), &v) != nil || !validRuntimeSettings(v) {
		return errors.New("运行模式配置无效")
	}
	s.runtime = v
	if err := writeRuntimeMode(s.stateDir, v); err != nil {
		return errors.New("运行模式文件无法准备")
	}
	if err := commitRuntimeMode(s.stateDir, v); err != nil {
		return errors.New("运行模式文件无法生效")
	}
	return nil
}

func (s *Store) runtimeFileMatchesLocked() bool {
	v, ok := runtimeFileState(s.stateDir)
	return ok && v.Mode == s.runtime.Mode && v.Revision == s.runtime.Revision
}
func (s *Store) logsEnabledLocked() bool {
	return s.runtime.Mode == runtimeNormal && s.runtimeFileMatchesLocked()
}
func (s *Store) publicRuntimeState() object {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	enabled := s.logsEnabledLocked()
	return object{"mode": s.runtime.Mode, "audit_enabled": enabled, "traffic_history_enabled": enabled, "subscription_access_enabled": enabled}
}
func (s *Store) markSubscriptionAccess(record *Record, at int64) error {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if !s.logsEnabledLocked() {
		return nil
	}
	record.LastSub = at
	return s.save(record)
}

func (s *Store) changeRuntimeMode(mode, revision string) (RuntimeSettings, int, error) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	old := s.runtime
	if mode != runtimeNormal && mode != runtimeNoLogs {
		return old, 400, errors.New("请选择常规模式或无日志模式")
	}
	if revision != old.Revision {
		return old, 409, errors.New("运行模式已更新，请刷新后重试")
	}
	if mode == old.Mode && s.runtimeFileMatchesLocked() {
		return old, 200, nil
	}
	next := RuntimeSettings{Mode: mode, Revision: randomToken(12), ChangedAt: time.Now().Unix()}
	// Prepare the file first. The old commit marker cannot enable this new
	// revision. Audit/account/access writers stay blocked under runtimeMu.
	if err := writeRuntimeMode(s.stateDir, next); err != nil {
		return old, 500, errors.New("运行模式文件写入失败，尚未生效")
	}
	b, _ := json.Marshal(next)
	if err := s.setMeta("runtime_settings", string(b)); err != nil {
		_ = writeRuntimeMode(s.stateDir, old)
		return old, 500, errors.New("运行模式保存失败，尚未生效")
	}
	s.runtime = next
	if err := commitRuntimeMode(s.stateDir, next); err != nil {
		return next, 500, errors.New("运行模式已保存但文件尚未生效，日志保持暂停，请重试")
	}
	if !s.runtimeFileMatchesLocked() {
		return next, 500, errors.New("运行模式文件校验失败，日志保持暂停，请重试")
	}
	return next, 200, nil
}

// The service manager still owns each wrapper PID. A child is never restarted
// inside a wrapper, so generation counters and restartCore(SIGTERM) retain their
// existing semantics. Verify the real MainPID command before claiming protection.
func runtimeCoreWrapperReady(unit, name string) bool {
	pid := corePID(unit)
	if pid < 2 {
		return false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return false
	}
	args := bytes.Split(bytes.TrimRight(b, "\x00"), []byte{0})
	return len(args) == 3 && string(args[0]) == "/opt/guangyue-personal/bin/guangyue" && string(args[1]) == "-run-core" && string(args[2]) == name
}
func (a *App) runtimeWrappersReady() bool {
	if a.cfg.Dev {
		return true
	}
	return os.Getenv("GUANGYUE_LOG_WRAPPER") == "panel" && runtimeCoreWrapperReady("guangyue-xray.service", "xray") && runtimeCoreWrapperReady("guangyue-hy2.service", "hy2")
}
func (a *App) runtimeSettings(w http.ResponseWriter, r *http.Request, actor Record) {
	if r.Method == "PUT" {
		var input struct {
			Mode     string `json:"mode"`
			Revision string `json:"revision"`
		}
		if !decode(w, r, &input) {
			return
		}
		if !a.runtimeWrappersReady() {
			failure(w, 503, "服务日志包装器尚未就绪，请完成服务更新")
			return
		}
		_, status, err := a.store.changeRuntimeMode(input.Mode, input.Revision)
		if err != nil {
			failure(w, status, err.Error())
			return
		}
		_ = a.cache.Invalidate(r.Context(), "dashboard:")
		if a.jobs != nil {
			_ = a.jobs.Prune(r.Context())
		}
		// Turning quiet never creates a new audit row; returning to normal is
		// recorded only after recording and output have both become active.
		a.store.audit(actor.Username, "runtime_mode", input.Mode)
	}
	wrappers := a.runtimeWrappersReady()
	a.store.runtimeMu.RLock()
	defer a.store.runtimeMu.RUnlock()
	v := a.store.runtime
	files := a.store.runtimeFileMatchesLocked()
	enabled := a.store.logsEnabledLocked()
	jsonResponse(w, 200, object{"mode": v.Mode, "revision": v.Revision, "changed_at": v.ChangedAt, "applied": files && wrappers, "audit_enabled": enabled, "traffic_history_enabled": enabled, "subscription_access_enabled": enabled, "application_logs_enabled": enabled, "core_logs_enabled": enabled, "mode_files_verified": files, "service_wrappers_verified": wrappers, "retains_existing_records": true, "retains_usage_accounting": true})
}

type runtimeLogWriter struct {
	dir string
	out io.Writer
	mu  sync.Mutex
}

func (w *runtimeLogWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	v, ok := runtimeFileState(w.dir)
	if !ok || v.Mode != runtimeNormal {
		return len(b), nil
	}
	return w.out.Write(b)
}

func (s *Store) runtimeSnapshot() RuntimeSettings {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.runtime
}
