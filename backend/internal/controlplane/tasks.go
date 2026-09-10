package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/jobs"
)

func (a *App) initTasks() {
	workers, capacity := 1, 64
	if a.cfg.controller() {
		workers, capacity = 4, 256
	}
	a.jobs = jobs.New(a.store.db, jobs.Options{Workers: workers, Capacity: capacity, Timeout: 3 * time.Minute, RetainHistory: func() bool {
		a.store.runtimeMu.RLock()
		defer a.store.runtimeMu.RUnlock()
		return a.store.logsEnabledLocked()
	}})
	for _, kind := range []string{"speed", "quality", "import-source", "public-source", "public-cycle", "core-apply"} {
		a.jobs.Register(kind, a.runTask)
	}
}
func (a *App) taskGuard(kind, target string) (string, error) {
	if kind == "core-apply" || kind == "public-cycle" {
		if target != "" {
			return "", errors.New("任务目标无效")
		}
		return "", nil
	}
	if kind == "import-source" {
		v, err := a.store.importSource(target)
		return v.Revision, err
	}
	if kind == "public-source" {
		catalog, err := a.store.publicSourceCatalog()
		if err != nil {
			return "", err
		}
		for _, s := range catalog {
			if s.ID == target && s.Enabled {
				b, _ := json.Marshal(s)
				return digest(string(b)), nil
			}
		}
		return "", errors.New("来源不存在或已停用")
	}
	parts := strings.Split(target, "/")
	if len(parts) != 2 || (parts[0] != "ips" && parts[0] != "nodes") {
		return "", errors.New("任务目标无效")
	}
	var n Node
	if parts[0] == "ips" {
		p, err := a.store.pool(parts[1])
		if err != nil {
			return "", err
		}
		n = p.Node
	} else {
		nodes, err := a.store.nodes()
		if err != nil {
			return "", err
		}
		for _, v := range nodes {
			if v.ID == parts[1] {
				n = v
				break
			}
		}
	}
	if n.ID == "" || !n.Enabled {
		return "", errors.New("资源不存在或已停用")
	}
	return nodeHash([]Node{n}, n.Protocol), nil
}
func (a *App) runTask(ctx context.Context, t jobs.Task) (json.RawMessage, error) {
	actor, err := a.store.record(t.ActorID)
	systemTask := t.ActorID == 0 && (a.cfg.businessAgent() && a.businessLeaseDeadline() > time.Now().Unix() && (t.Kind == "speed" || t.Kind == "quality") || t.Kind == "import-source" || t.Kind == "public-source" || t.Kind == "public-cycle")
	if !systemTask && (err != nil || !actor.Enabled || actor.Role != "owner") {
		return nil, errors.New("任务发起者已无管理权限")
	}
	guard, err := a.taskGuard(t.Kind, t.Target)
	if err != nil || guard != t.Guard {
		return nil, errors.New("资源已变更或删除，请重新发起任务")
	}
	var result any
	status := 200
	switch t.Kind {
	case "speed", "quality":
		parts := strings.Split(t.Target, "/")
		if t.Kind == "speed" {
			result, status, err = a.measureResourceSpeed(ctx, parts[0] == "ips", parts[1], actor)
		} else {
			result, status, err = a.measureResourceQuality(ctx, parts[0] == "ips", parts[1])
		}
		if status == 409 && err != nil && strings.Contains(err.Error(), "正在") {
			return nil, jobs.ErrBusy
		}
		if status == 409 && err != nil && strings.Contains(err.Error(), "繁忙") {
			return nil, jobs.ErrBusy
		}
	case "core-apply":
		if !a.heavyMu.TryLock() {
			return nil, jobs.ErrBusy
		}
		defer a.heavyMu.Unlock()
		a.mu.Lock()
		err = a.reconcile()
		a.mu.Unlock()
		result = object{"ok": err == nil}
		if err != nil {
			return nil, errors.New("核心配置应用失败，请查看运行状态")
		}
	case "import-source":
		a.mu.Lock()
		source, e := a.store.importSource(t.Target)
		if e == nil {
			source.Queued = true
			e = a.store.saveImportSource(source)
		}
		a.mu.Unlock()
		if e != nil {
			return nil, errors.New("订阅来源不存在")
		}
		if !a.runImportSourceID(ctx, fetchSubscription, time.Now().Unix(), t.Target) {
			return nil, jobs.ErrBusy
		}
		source, e = a.store.importSource(t.Target)
		if e != nil {
			return nil, errors.New("订阅来源已删除")
		}
		if source.Error != "" {
			return nil, errors.New("订阅更新失败，请查看来源状态")
		}
		result = object{"updated": true}
	case "public-source":
		a.publicSourceMu.Lock()
		state := a.store.publicSourceState(t.Target)
		state.Queued = true
		err = a.store.savePublicSourceState(state)
		a.publicSourceMu.Unlock()
		if err != nil {
			return nil, errors.New("来源排队失败")
		}
		if !a.runPublicSourceID(ctx, func(ctx context.Context, u string) ([]byte, error) { return a.fetchPublicSourceMode(ctx, u, true, nil) }, time.Now().Unix(), t.Target) {
			return nil, jobs.ErrBusy
		}
		state = a.store.publicSourceState(t.Target)
		if state.Error != "" {
			return nil, errors.New("来源更新失败，请查看来源状态")
		}
		result = object{"updated": true}
	case "public-cycle":
		if err = a.startPublicCycle(true); err != nil {
			return nil, jobs.ErrBusy
		}
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			a.publicMu.Lock()
			state := a.publicStatus
			cancel := a.publicCancel
			a.publicMu.Unlock()
			if !state.Running {
				if state.Error != "" {
					return nil, errors.New("公开采集失败，请查看采集状态")
				}
				result = object{"checked": state.Checked, "added": state.Added, "removed": state.Removed}
				break
			}
			select {
			case <-ctx.Done():
				if cancel != nil {
					cancel()
				}
				return nil, ctx.Err()
			case <-ticker.C:
			}
		}
	default:
		return nil, errors.New("任务类型无效")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
func (a *App) tasksAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	if a.jobs == nil {
		failure(w, 503, "任务中心尚未启动")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/tasks")
	if path == "" && r.Method == "GET" {
		v, err := a.jobs.List(r.Context())
		if err != nil {
			failure(w, 500, "读取任务失败")
			return
		}
		jsonResponse(w, 200, object{"items": v})
		return
	}
	if path == "" && r.Method == "POST" {
		var in struct {
			Kind   string `json:"kind"`
			Target string `json:"target"`
		}
		if !decode(w, r, &in) {
			return
		}
		guard, err := a.taskGuard(in.Kind, in.Target)
		if err != nil {
			failure(w, 400, "资源不存在、已停用或任务目标无效")
			return
		}
		task, err := a.jobs.Submit(r.Context(), in.Kind, in.Target, guard, actor.ID)
		if err != nil {
			if errors.Is(err, jobs.ErrFull) {
				failure(w, 429, "任务队列已满，请稍后重试")
			} else {
				failure(w, 400, "任务提交失败")
			}
			return
		}
		jsonResponse(w, 202, task)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 1 && r.Method == "GET" {
		v, err := a.jobs.Get(r.Context(), parts[0])
		if err != nil {
			failure(w, 404, "任务不存在")
			return
		}
		jsonResponse(w, 200, v)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == "POST" {
		if err := a.jobs.Cancel(r.Context(), parts[0]); err != nil {
			failure(w, 500, "取消任务失败")
			return
		}
		jsonResponse(w, 200, object{"ok": true})
		return
	}
	failure(w, 404, "接口不存在")
}

// Schedules only bounded due work. Dispatch jitter/backoff remains in each source
// service, while durable queue state is shared by automatic and manual requests.
func (a *App) enqueueSystem(ctx context.Context, kind, target string) error {
	guard, err := a.taskGuard(kind, target)
	if err != nil {
		return err
	}
	_, err = a.jobs.Submit(ctx, kind, target, guard, 0)
	return err
}
func (a *App) scheduleTasks(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now().Unix()
		a.mu.Lock()
		sources, _ := a.store.importSources()
		next, _ := strconv.ParseInt(a.store.meta("import_next_dispatch"), 10, 64)
		a.mu.Unlock()
		sort.Slice(sources, func(i, j int) bool {
			if sources[i].Queued != sources[j].Queued {
				return sources[i].Queued
			}
			return sources[i].NextAt < sources[j].NextAt
		})
		if now >= next {
			for _, s := range sources {
				if s.Queued || s.Enabled && s.NextAt <= now {
					_ = a.enqueueSystem(ctx, "import-source", s.ID)
					break
				}
			}
		}
		pending := false
		for _, v := range a.publicSourceStates() {
			pending = pending || v.Queued || v.Running
			if v.Queued && !v.Running && now >= v.LastAttempt+60 {
				_ = a.enqueueSystem(ctx, "public-source", v.ID)
				break
			}
		}
		if a.cfg.Dev {
			continue
		}
		a.publicMu.Lock()
		c := a.store.publicSettings()
		previous := a.publicStatus
		if previous.StartedAt == 0 {
			_ = json.Unmarshal([]byte(a.store.meta("public_status")), &previous)
		}
		running := a.publicCancel != nil
		a.publicMu.Unlock()
		screen := a.store.meta("public_source_screen_pending") == "1"
		due := previous.FinishedAt == 0 || now-previous.FinishedAt >= int64(c.IntervalMinutes)*60
		if c.Enabled && !running && !pending && (due || screen) {
			if a.enqueueSystem(ctx, "public-cycle", "") == nil && screen {
				_ = a.store.setMeta("public_source_screen_pending", "")
			}
		}
	}
}
