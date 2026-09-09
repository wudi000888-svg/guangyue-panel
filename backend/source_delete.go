package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type sourceDeletion struct {
	source                                            ImportSource
	removedPools, reassignedPools, oldReassignedPools []IPResource
	removedNodes                                      []Node
	oldRecords, newRecords                            []Record
}

func (a *App) planSourceDeletion(source ImportSource) (sourceDeletion, error) {
	p := sourceDeletion{source: source}
	pools, err := a.store.pools()
	if err != nil {
		return p, err
	}
	sources, err := a.store.importSources()
	if err != nil {
		return p, err
	}
	remove := map[string]bool{}
	for _, resource := range pools {
		// Membership also covers existing imports adopted by a managed source.
		if resource.PoolGroup == "public" || resource.SubscriptionID != source.ID && !containsSourceID(source.ResourceIDs, resource.ID) {
			continue
		}
		otherID := ""
		for _, other := range sources {
			if other.ID != source.ID && (resource.SubscriptionID == other.ID || containsSourceID(other.ResourceIDs, resource.ID)) {
				otherID = other.ID
				break
			}
		}
		if otherID != "" {
			p.oldReassignedPools = append(p.oldReassignedPools, resource)
			resource.SubscriptionID = otherID
			resource.Revision = randomToken(12)
			resource.SourceStale = false
			p.reassignedPools = append(p.reassignedPools, resource)
			continue
		}
		remove[resource.ID] = true
		p.removedPools = append(p.removedPools, resource)
	}
	nodes, err := a.store.nodes()
	if err != nil {
		return p, err
	}
	removeNodes := map[string]bool{}
	for _, n := range nodes {
		if remove[n.ExitID] {
			if n.DefaultDirect {
				return p, errors.New(defaultDirectMessage)
			}
			p.removedNodes = append(p.removedNodes, n)
			removeNodes[n.ID] = true
		}
	}
	records, err := a.store.records()
	if err != nil {
		return p, err
	}
	for _, record := range records {
		credentials := map[string]string{}
		changed := false
		for id, key := range record.Credentials.VLESS {
			if removeNodes[id] {
				changed = true
			} else {
				credentials[id] = key
			}
		}
		if changed {
			p.oldRecords = append(p.oldRecords, record)
			record.Credentials.VLESS = credentials
			p.newRecords = append(p.newRecords, record)
		}
	}
	return p, nil
}

// Membership, configuration and credentials commit together. The caller holds
// a.mu and heavyMu, so a failed core apply can restore only these affected rows.
func (s *Store) persistSourceDeletion(p sourceDeletion, restore bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	put := func(table, id string, value any) error {
		b, e := s.vault.seal(value)
		if e != nil {
			return e
		}
		_, e = tx.Exec("INSERT INTO "+table+"(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", id, b)
		return e
	}
	if restore {
		b, e := s.vault.seal(p.source)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO import_sources(id,url_hash,doc) VALUES(?,?,?)", p.source.ID, digest(p.source.URL), b); e != nil {
			return e
		}
		for _, resource := range append(append([]IPResource{}, p.removedPools...), p.oldReassignedPools...) {
			if e = put("ip_pool", resource.ID, resource); e != nil {
				return e
			}
		}
		for _, n := range p.removedNodes {
			if e = put("nodes", n.ID, n); e != nil {
				return e
			}
		}
	} else {
		for _, n := range p.removedNodes {
			if _, err = tx.Exec("DELETE FROM nodes WHERE id=?", n.ID); err != nil {
				return err
			}
		}
		for _, resource := range p.removedPools {
			if _, err = tx.Exec("DELETE FROM ip_pool WHERE id=?", resource.ID); err != nil {
				return err
			}
		}
		for _, resource := range p.reassignedPools {
			if err = put("ip_pool", resource.ID, resource); err != nil {
				return err
			}
		}
		result, e := tx.Exec("DELETE FROM import_sources WHERE id=?", p.source.ID)
		if e != nil {
			return e
		}
		count, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if count != 1 {
			return sql.ErrNoRows
		}
	}
	records := p.newRecords
	if restore {
		records = p.oldRecords
	}
	for _, record := range records {
		b, e := s.vault.seal(record.Credentials)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE users SET credentials=? WHERE id=?", b, record.ID); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (a *App) applySourceDeletion(p sourceDeletion, apply func() error) error {
	if err := a.store.persistSourceDeletion(p, false); err != nil {
		return errors.New("删除订阅来源失败，原配置未改变")
	}
	if err := apply(); err != nil {
		a.status = "error"
		a.syncError = "subscription deletion apply failed"
		if e := a.store.persistSourceDeletion(p, true); e != nil {
			return errors.New("删除应用失败且数据恢复未完成，请检查运行状态")
		}
		if e := apply(); e != nil {
			return errors.New("删除未完成，已恢复原配置，核心同步等待重试")
		}
		a.status = "applied"
		a.syncError = ""
		return errors.New("删除应用失败，已恢复订阅来源、出口与节点")
	}
	a.status = "applied"
	a.syncError = ""
	a.appliedAt = time.Now().Unix()
	return nil
}

func (a *App) deleteImportSource(w http.ResponseWriter, r *http.Request, actor Record, source ImportSource) {
	if !a.heavyMu.TryLock() {
		failure(w, 409, "资源任务正在运行，请稍后重试删除")
		return
	}
	defer a.heavyMu.Unlock()
	p, err := a.planSourceDeletion(source)
	if err != nil {
		failure(w, 500, "读取订阅关联资源失败")
		return
	}
	if len(p.removedNodes) > 0 && !a.cfg.Dev {
		if err = a.collect(); err != nil {
			failure(w, 502, "流量同步暂未完成，请稍后重试删除")
			return
		}
	}
	if err = a.applySourceDeletion(p, a.reconcile); err != nil {
		failure(w, 502, err.Error())
		return
	}
	a.store.audit(actor.Username, "delete-import-source", fmt.Sprintf("%s resources=%d nodes=%d", source.ID, len(p.removedPools), len(p.removedNodes)))
	jsonResponse(w, 200, object{"ok": true, "resources_deleted": len(p.removedPools), "nodes_deleted": len(p.removedNodes), "shared_resources_retained": len(p.reassignedPools)})
}
