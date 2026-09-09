package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type PublicSourceState struct {
	ID          string `json:"id"`
	Queued      bool   `json:"queued"`
	Running     bool   `json:"running"`
	LastAttempt int64  `json:"last_attempt"`
	LastSuccess int64  `json:"last_success"`
	Entries     int    `json:"entries"`
	Bytes       int    `json:"bytes"`
	HTTPStatus  int    `json:"http_status"`
	Error       string `json:"error"`
}

func (s *Store) publicSourceState(id string) PublicSourceState {
	v := PublicSourceState{ID: id}
	var doc string
	if s.db.QueryRow("SELECT doc FROM public_source_tasks WHERE id=?", id).Scan(&doc) == nil {
		_ = json.Unmarshal([]byte(doc), &v)
	}
	return v
}
func (s *Store) savePublicSourceState(v PublicSourceState) error {
	v.Running = false
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO public_source_tasks(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", v.ID, string(b))
	return err
}
func (a *App) publicSourceStates() []PublicSourceState {
	a.publicSourceMu.Lock()
	defer a.publicSourceMu.Unlock()
	out := []PublicSourceState{}
	catalog, _ := a.store.publicSourceCatalog()
	for _, source := range catalog {
		v := a.store.publicSourceState(source.ID)
		v.Running = a.publicSourceID == v.ID
		out = append(out, v)
	}
	return out
}
func (a *App) queuePublicSources(w http.ResponseWriter, r *http.Request, actor Record) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.publicSourceMu.Lock()
	defer a.publicSourceMu.Unlock()
	catalog, err := a.store.publicSourceCatalog()
	if err != nil {
		failure(w, 500, "无法读取公开来源")
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > len(catalog) {
		failure(w, 400, "请选择要抓取的公开来源")
		return
	}
	seen := map[string]bool{}
	for _, id := range in.IDs {
		valid := false
		for _, s := range catalog {
			if s.ID == id && s.Enabled && s.Kind != "text" {
				valid = true
			}
		}
		if !valid || seen[id] {
			failure(w, 400, "请选择有效且不重复的公开来源")
			return
		}
		seen[id] = true
	}
	tx, err := a.store.db.Begin()
	if err != nil {
		failure(w, 500, "公开来源排队失败")
		return
	}
	defer tx.Rollback()
	// Keep all selected sources in one transaction, including concurrent queued requests.
	for _, id := range in.IDs {
		if a.publicSourceID == id {
			continue
		}
		v := PublicSourceState{ID: id}
		var doc string
		if tx.QueryRow("SELECT doc FROM public_source_tasks WHERE id=?", id).Scan(&doc) == nil {
			_ = json.Unmarshal([]byte(doc), &v)
		}
		v.Queued = true
		b, _ := json.Marshal(v)
		if _, err = tx.Exec("INSERT INTO public_source_tasks(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", id, string(b)); err != nil {
			failure(w, 500, "公开来源排队失败")
			return
		}
	}
	if tx.Commit() != nil {
		failure(w, 500, "公开来源排队失败")
		return
	}
	a.store.audit(actor.Username, "fetch_public_sources", strconv.Itoa(len(in.IDs)))
	jsonResponse(w, 202, object{"queued": true, "count": len(in.IDs)})
}

// Shared by periodic and manual fetches; failed refreshes preserve the last valid cache.
func (a *App) recordPublicSource(id string, attempt bool, body []byte, status int, err error) {
	a.publicSourceMu.Lock()
	defer a.publicSourceMu.Unlock()
	v := a.store.publicSourceState(id)
	if attempt {
		v.LastAttempt = time.Now().Unix()
	} else if err != nil {
		v.Error = err.Error()
		v.HTTPStatus = status
	} else {
		v.LastSuccess = time.Now().Unix()
		v.Error = ""
		v.Bytes = len(body)
		v.HTTPStatus = status
		catalog, _ := a.store.publicSourceCatalog()
		for _, source := range catalog {
			if source.ID == id {
				v.Entries = len(parsePublicList(body, source))
				break
			}
		}
	}
	_ = a.store.savePublicSourceState(v)
}

func (a *App) runPublicSource(ctx context.Context, fetch func(context.Context, string) ([]byte, error), now int64) bool {
	return a.runPublicSourceID(ctx, fetch, now, "")
}
func (a *App) runPublicSourceID(ctx context.Context, fetch func(context.Context, string) ([]byte, error), now int64, onlyID string) bool {
	if !a.heavyMu.TryLock() {
		return false
	}
	defer a.heavyMu.Unlock()
	a.publicSourceMu.Lock()
	next, _ := strconv.ParseInt(a.store.meta("public_source_dispatch"), 10, 64)
	if now < next {
		a.publicSourceMu.Unlock()
		return false
	}
	var selected PublicSource
	catalog, err := a.store.publicSourceCatalog()
	if err != nil {
		a.publicSourceMu.Unlock()
		return false
	}
	for _, source := range catalog {
		v := a.store.publicSourceState(source.ID)
		if (onlyID == "" || source.ID == onlyID) && v.Queued && source.Enabled && source.Kind != "text" && now >= v.LastAttempt+60 {
			selected = source
			break
		}
	}
	if selected.ID == "" {
		a.publicSourceMu.Unlock()
		return false
	}
	v := a.store.publicSourceState(selected.ID)
	v.LastAttempt = now
	if a.store.savePublicSourceState(v) != nil {
		a.publicSourceMu.Unlock()
		return false
	}
	a.publicSourceID = selected.ID
	_ = a.store.setMeta("public_source_dispatch", strconv.FormatInt(now+15, 10))
	a.publicSourceMu.Unlock()
	_, err = fetch(ctx, selected.URL)
	a.publicSourceMu.Lock()
	v = a.store.publicSourceState(selected.ID)
	v.Queued = ctx.Err() != nil
	if err != nil {
		v.Error = err.Error()
	}
	_ = a.store.savePublicSourceState(v)
	a.publicSourceID = ""
	a.publicSourceMu.Unlock()
	c := a.store.publicSettings()
	if err == nil && ctx.Err() == nil && c.Enabled && containsSourceID(c.Sources, selected.ID) {
		_ = a.store.setMeta("public_source_screen_pending", "1")
	}
	return true
}
func (a *App) publicSourceLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a.runPublicSource(ctx, func(ctx context.Context, u string) ([]byte, error) { return a.fetchPublicSourceMode(ctx, u, true, nil) }, time.Now().Unix()) {
				continue
			}
			if a.store.meta("public_source_screen_pending") == "1" {
				pending := false
				for _, v := range a.publicSourceStates() {
					pending = pending || v.Queued || v.Running
				}
				if !a.store.publicSettings().Enabled {
					_ = a.store.setMeta("public_source_screen_pending", "")
				} else if !pending && !a.cfg.Dev {
					if a.startPublicCycle(true) == nil {
						_ = a.store.setMeta("public_source_screen_pending", "")
					}
				}
			}
		}
	}
}
