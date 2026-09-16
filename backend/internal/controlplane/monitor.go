package controlplane

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Samples are ephemeral and never participate in quota accounting. Keep five
// minutes at the nominal interval, bounded additionally by stored user points.
const liveFrames = 60
const livePointBudget = 20000

type liveInput struct {
	at          time.Time
	traffic     [2]bool
	connections [2]bool
	generation  [2]string
	counts      [2]map[int64]int64
	counters    []Counter
}
type liveValue struct {
	Upload   *float64 `json:"upload_rate"`
	Download *float64 `json:"download_rate"`
	VLESS    *int64   `json:"vless"`
	HY2      *int64   `json:"hy2"`
}
type liveFrame struct {
	At          int64     `json:"at"`
	Traffic     [2]bool   `json:"traffic_available"`
	Rates       [2]bool   `json:"rates_available"`
	Connections [2]bool   `json:"connections_available"`
	Total       liveValue `json:"total"`
	values      map[int64]liveValue
}
type liveMonitor struct {
	sync.RWMutex
	previous liveInput
	frames   []liveFrame
}

func pointer[T any](v T) *T { return &v }
func (m *liveMonitor) sample(in liveInput) {
	m.Lock()
	defer m.Unlock()
	f := liveFrame{At: in.at.UnixMilli(), Traffic: in.traffic, Connections: in.connections, values: map[int64]liveValue{}}
	elapsed := in.at.Sub(m.previous.at).Seconds()
	for p := range 2 {
		f.Rates[p] = in.traffic[p] && m.previous.traffic[p] && elapsed > 0 && elapsed <= 15 && in.generation[p] == m.previous.generation[p]
	}
	defaults := func() liveValue {
		v := liveValue{}
		if f.Rates[0] && f.Rates[1] {
			v.Upload, v.Download = pointer(0.0), pointer(0.0)
		}
		if in.connections[0] {
			v.VLESS = pointer(int64(0))
		}
		if in.connections[1] {
			v.HY2 = pointer(int64(0))
		}
		return v
	}
	get := func(id int64) liveValue {
		if v, ok := f.values[id]; ok {
			return v
		}
		return defaults()
	}
	previous := make(map[string]Counter, len(m.previous.counters))
	for _, c := range m.previous.counters {
		previous[c.Key] = c
	}
	for _, c := range in.counters {
		if c.UserID <= 0 {
			continue
		}
		v := get(c.UserID)
		old, exists := previous[c.Key]
		if v.Upload != nil {
			if c.Value < 0 || exists && (c.Generation != old.Generation || c.Value < old.Value) {
				v.Upload, v.Download = nil, nil
			} else {
				delta := c.Value
				if exists {
					delta -= old.Value
				}
				if c.Direction == "up" {
					*v.Upload += float64(delta) / elapsed
				} else {
					*v.Download += float64(delta) / elapsed
				}
			}
		}
		f.values[c.UserID] = v
	}
	for p, counts := range in.counts {
		if !in.connections[p] {
			continue
		}
		for id, count := range counts {
			if id <= 0 || count < 0 {
				continue
			}
			v := get(id)
			if p == 0 {
				v.VLESS = pointer(count)
			} else {
				v.HY2 = pointer(count)
			}
			f.values[id] = v
		}
	}
	f.Total = defaults()
	for _, v := range f.values {
		if v.Upload == nil {
			f.Total.Upload, f.Total.Download = nil, nil
		}
		if f.Total.Upload != nil {
			*f.Total.Upload += *v.Upload
			*f.Total.Download += *v.Download
		}
		if f.Total.VLESS != nil && v.VLESS != nil {
			*f.Total.VLESS += *v.VLESS
		}
		if f.Total.HY2 != nil && v.HY2 != nil {
			*f.Total.HY2 += *v.HY2
		}
	}
	m.previous = in
	m.frames = append(m.frames, f)
	points, start := 0, len(m.frames)-1
	for start >= 0 {
		points += len(m.frames[start].values)
		if len(m.frames)-start > liveFrames || f.At-m.frames[start].At > (5*time.Minute).Milliseconds() || points > livePointBudget && start < len(m.frames)-1 {
			break
		}
		start--
	}
	if start >= 0 {
		m.frames = append([]liveFrame(nil), m.frames[start+1:]...)
	}
}

func frameUser(f liveFrame, id int64) liveValue {
	if v, ok := f.values[id]; ok {
		return v
	}
	v := liveValue{}
	if f.Rates[0] && f.Rates[1] {
		v.Upload, v.Download = pointer(0.0), pointer(0.0)
	}
	if f.Connections[0] {
		v.VLESS = pointer(int64(0))
	}
	if f.Connections[1] {
		v.HY2 = pointer(int64(0))
	}
	return v
}

func (a *App) liveAPI(w http.ResponseWriter, r *http.Request, actor Record) {
	if actor.Role != "owner" {
		failure(w, 403, "需要管理员权限")
		return
	}
	uid, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if r.URL.Query().Get("user_id") == "" {
		uid, err = 0, nil
	}
	page, pe := strconv.Atoi(r.URL.Query().Get("page"))
	if r.URL.Query().Get("page") == "" {
		page, pe = 1, nil
	}
	if err != nil || uid < 0 || pe != nil || page < 1 || page > 100000 {
		failure(w, 400, "监控筛选无效")
		return
	}
	type liveUser struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		liveValue
	}
	// Read only public user fields; never decrypt credentials for a live poll.
	rows, err := a.store.db.Query("SELECT id,username FROM users ORDER BY id")
	if err != nil {
		failure(w, 500, "读取用户失败")
		return
	}
	users := []liveUser{}
	found := uid == 0
	for rows.Next() {
		var u liveUser
		if err = rows.Scan(&u.ID, &u.Username); err != nil {
			break
		}
		found = found || u.ID == uid
		users = append(users, u)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		failure(w, 500, "读取用户失败")
		return
	}
	if !found {
		failure(w, 404, "用户不存在")
		return
	}
	a.monitor.RLock()
	// Frames are immutable after publication. Release the sampler lock before
	// filtering, encoding or writing to a potentially slow browser connection.
	frames := append([]liveFrame(nil), a.monitor.frames...)
	a.monitor.RUnlock()
	f := liveFrame{}
	if len(frames) > 0 {
		f = frames[len(frames)-1]
	}
	stale := f.At == 0 || time.Now().UnixMilli()-f.At > 15000
	history := []object{}
	for _, frame := range frames {
		value := frame.Total
		if uid > 0 {
			value = frameUser(frame, uid)
		}
		history = append(history, object{"at": frame.At, "upload_rate": value.Upload, "download_rate": value.Download})
	}
	filtered := users[:0]
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	for _, u := range users {
		if !strings.Contains(strings.ToLower(u.Username), query) {
			continue
		}
		u.liveValue = frameUser(f, u.ID)
		if stale {
			u.liveValue = liveValue{}
		}
		filtered = append(filtered, u)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		score := func(u liveUser) float64 {
			var n float64
			if u.Upload != nil {
				n += *u.Upload + *u.Download
			}
			return n
		}
		return score(filtered[i]) > score(filtered[j])
	})
	total := len(filtered)
	start := min((page-1)*100, total)
	value := f.Total
	if uid > 0 {
		value = frameUser(f, uid)
	}
	if stale {
		value, f.Total = liveValue{}, liveValue{}
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonResponse(w, 200, object{"site_id": a.cfg.siteID(), "scope": "local", "sampled_at": f.At, "interval_seconds": 5, "stale": stale, "traffic_available": f.Traffic, "connections_available": f.Connections, "rates_available": f.Rates, "total": f.Total, "selected": value, "user_id": uid, "users": filtered[start:min(start+100, total)], "user_count": total, "page": page, "history": history, "ephemeral": true})
}
