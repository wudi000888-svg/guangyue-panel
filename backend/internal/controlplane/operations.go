package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"
)

type CoreRevision struct {
	Desired   int64  `json:"desired"`
	Applied   int64  `json:"applied"`
	State     string `json:"state"`
	Changed   int64  `json:"changed"`
	AppliedAt int64  `json:"applied_at"`
	Error     string `json:"error,omitempty"`
}

func (s *Store) coreRevision() CoreRevision {
	v := CoreRevision{State: "pending"}
	_ = s.db.QueryRow("SELECT desired,applied,state,changed,applied_at,error FROM core_revisions WHERE id=1").Scan(&v.Desired, &v.Applied, &v.State, &v.Changed, &v.AppliedAt, &v.Error)
	return v
}
func (a *App) operations(w http.ResponseWriter, r *http.Request, actor Record) {
	a.mu.Lock()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	system := object{"version": version, "edition": a.cfg.edition(), "site_id": a.cfg.siteID(), "database": a.store.db.Driver(), "cache": a.cache.Mode(), "cache_degraded": a.cache.Degraded(), "status": a.status, "error": "", "traffic_error": "", "applied_at": a.appliedAt, "heap": mem.HeapAlloc, "uptime": int64(time.Since(a.started).Seconds())}
	if actor.Role == "owner" {
		system["error"] = a.syncError
		system["traffic_error"] = a.trafficError
		system["configuration"] = a.store.coreRevision()
	}
	a.mu.Unlock()
	result := object{"system": system, "me": actor.User, "site": a.store.siteSettings(), "runtime": a.store.publicRuntimeState(), "unread_messages": a.store.unreadMessages(actor.ID)}
	if actor.Role == "owner" {
		counts := map[string]int{}
		rows, err := a.store.db.Query("SELECT state,COUNT(*) FROM tasks GROUP BY state")
		if err == nil {
			for rows.Next() {
				var state string
				var count int
				if rows.Scan(&state, &count) == nil {
					counts[state] = count
				}
			}
			rows.Close()
		}
		result["tasks"] = counts
		stats := a.store.db.Stats()
		result["database_pool"] = object{"open": stats.OpenConnections, "in_use": stats.InUse, "wait_count": stats.WaitCount}
	}
	jsonResponse(w, 200, result)
}

// All callers already hold the control-plane mutex. Hashing excludes transfer
// counters and probe metadata; expiration/quota changes still alter active users.
func (a *App) desiredCoreHash() (string, error) {
	nodes, err := a.store.nodes()
	if err != nil {
		return "", err
	}
	records, err := a.coreRecords()
	if err != nil {
		return "", err
	}
	users := []object{}
	for _, u := range records {
		users = append(users, object{"id": u.ID, "active": u.Active(), "vless": u.VLESS, "hy2": u.HY2, "credentials": u.Credentials})
	}
	b, err := json.Marshal(object{"hy2_optimized": a.cfg.HY2Optimized, "vless": nodeHash(nodes, "vless"), "hy2": nodeHash(nodes, "hy2"), "users": users, "sni": a.cfg.RealitySNI, "target": a.cfg.RealityTarget})
	return digest(string(b)), err
}
func (a *App) reconcileIfNeeded() error {
	hash, err := a.desiredCoreHash()
	if err != nil {
		return err
	}
	if hash == a.lastCoreHash && time.Since(a.lastCoreCheck) < 30*time.Second {
		return nil
	}
	return a.reconcile()
}
func (a *App) reconcile() (err error) {
	if err = a.controlReady(); err != nil {
		return err
	}
	hash, err := a.desiredCoreHash()
	if err != nil {
		return err
	}
	previousHash := a.store.meta("desired_core_hash")
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO core_revisions(id,desired,applied,state) VALUES(1,0,0,'pending') ON CONFLICT(id) DO NOTHING"); err != nil {
		return err
	}
	if hash != previousHash {
		if _, err = tx.Exec("UPDATE core_revisions SET desired=desired+1,state='pending',changed=?,error='' WHERE id=1", time.Now().Unix()); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES('desired_core_hash',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", hash); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = a.applyCoreConfiguration(); err != nil {
		_, _ = a.store.db.Exec("UPDATE core_revisions SET state='failed',error='核心配置应用失败' WHERE id=1")
		return err
	}
	if _, err = a.store.db.Exec("UPDATE core_revisions SET applied=desired,state='applied',applied_at=?,error='' WHERE id=1", time.Now().Unix()); err != nil {
		return err
	}
	a.lastCoreHash = hash
	a.lastCoreCheck = time.Now()
	return nil
}

func (a *App) controlReady() error {
	parent := a.publicContext
	if parent == nil {
		parent = context.Background()
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	return a.controllerLease.Check(ctx)
}
