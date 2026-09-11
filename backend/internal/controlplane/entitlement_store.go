package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

const legacyPrivateGroup = "legacy-private"
const legacyPublicGroup = "legacy-public"

type NodeGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
	Sort        int    `json:"sort"`
	Revision    string `json:"revision"`
}
type Plan struct {
	ID          string   `json:"id"`
	Version     int      `json:"version"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Notes       string   `json:"notes"`
	Archived    bool     `json:"archived"`
	Sort        int      `json:"sort"`
	Quota       int64    `json:"quota"`
	ValidDays   int      `json:"valid_days"`
	Cycle       string   `json:"cycle"`
	Timezone    string   `json:"timezone"`
	GroupIDs    []string `json:"group_ids"`
	VLESS       bool     `json:"vless"`
	HY2         bool     `json:"hy2"`
	// Price is the package's default catalog price in integer CNY cents.
	Price int64 `json:"price,string,omitempty"`
}

func normalizeNodePolicy(n Node) Node {
	if n.PolicyVersion == 0 {
		n.PolicyVersion, n.RateMilli, n.RateRevision = 1, 1000, "legacy-1"
		n.GroupIDs = []string{legacyPrivateGroup}
		if n.ManagedBy == publicManager {
			n.GroupIDs = []string{legacyPublicGroup}
		}
	}
	if n.GroupIDs == nil {
		n.GroupIDs = []string{}
	}
	return n
}
func nodeRate(n Node) int64 { return normalizeNodePolicy(n).RateMilli }

func readDocuments[T any](s *Store, table string) ([]T, error) {
	// The table names are package-owned constants, never request input.
	rows, err := s.db.Query("SELECT doc FROM " + table + " ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var b []byte
		var v T
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) nodeGroups() ([]NodeGroup, error) { return readDocuments[NodeGroup](s, "node_groups") }
func (s *Store) plans() ([]Plan, error)           { return readDocuments[Plan](s, "plans") }
func (s *Store) plan(id string) (Plan, error) {
	var b []byte
	var p Plan
	err := s.db.QueryRow("SELECT doc FROM plans WHERE id=?", id).Scan(&b)
	if err == nil {
		err = json.Unmarshal(b, &p)
	}
	return p, err
}
func (s *Store) initEntitlements() error {
	done, err := s.readMeta("entitlements_v1")
	if err != nil || done == "1" {
		return err
	}
	users, err := s.records()
	if err != nil {
		return err
	}
	nodes, err := s.nodes()
	if err != nil {
		return err
	}
	sites, err := s.businessSites()
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, g := range []NodeGroup{{ID: legacyPrivateGroup, Name: "默认普通节点", Scope: "private", Enabled: true, Revision: "1"}, {ID: legacyPublicGroup, Name: "默认公共节点", Scope: "public", Enabled: true, Revision: "1"}} {
		b, _ := json.Marshal(g)
		if _, err = tx.Exec("INSERT INTO node_groups(id,doc) VALUES(?,?) ON CONFLICT(id) DO NOTHING", g.ID, b); err != nil {
			return err
		}
	}
	for _, u := range users {
		u.InitMeter("legacy-"+businessUsageKey(u.ID), time.Now().Unix())
		b, _ := json.Marshal(u.User)
		if _, err = tx.Exec("UPDATE users SET doc=? WHERE id=?", b, u.ID); err != nil {
			return err
		}
	}
	for _, n := range nodes {
		b, e := s.vault.seal(normalizeNodePolicy(n))
		if e != nil {
			return e
		}
		if _, err = tx.Exec("UPDATE nodes SET doc=? WHERE id=?", b, n.ID); err != nil {
			return err
		}
	}
	for _, site := range sites {
		for _, n := range append(append([]Node{}, site.SentNodes...), site.IssuedNodes...) {
			n = normalizeNodePolicy(n)
			site.RateRules[n.ID+"/"+n.RateRevision] = n.RateMilli
		}
		for _, u := range users {
			for _, g := range site.Grants {
				if g.UserID == u.ID {
					site.PeriodRules[businessUsageKey(u.ID)+"/legacy-"+businessUsageKey(u.ID)] = true
				}
			}
		}
		encoded, e := s.vault.seal(site)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE business_sites SET doc=? WHERE id=?", encoded, site.ID); e != nil {
			return e
		}
	}
	if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES('entitlements_v1','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) resolveAccess(records []Record) error {
	groups, err := s.nodeGroups()
	if err != nil {
		return err
	}
	enabled := map[string]bool{}
	for _, g := range groups {
		enabled[g.ID] = g.Enabled
	}
	for i := range records {
		r := &records[i]
		r.AccessResolved = true
		r.AllowedGroups = []string{}
		ids := []string{legacyPrivateGroup, legacyPublicGroup}
		if r.Entitlement != nil {
			ids = r.Entitlement.GroupIDs
		}
		for _, id := range ids {
			if enabled[id] {
				r.AllowedGroups = append(r.AllowedGroups, id)
			}
		}
	}
	return nil
}
func nodeGroupAllowed(r Record, n Node) bool {
	ids := r.AllowedGroups
	if !r.AccessResolved && r.CompiledGroups != nil {
		ids = *r.CompiledGroups
	} else if !r.AccessResolved {
		ids = []string{legacyPrivateGroup, legacyPublicGroup}
		if r.Entitlement != nil {
			ids = r.Entitlement.GroupIDs
		}
	}
	for _, group := range normalizeNodePolicy(n).GroupIDs {
		for _, id := range ids {
			if group == id {
				return true
			}
		}
	}
	return false
}

func (s *Store) validateNodePolicy(n *Node, old *Node) error {
	if n.PolicyVersion == 0 {
		if old != nil {
			n.PolicyVersion, n.RateMilli, n.RateRevision, n.GroupIDs = old.PolicyVersion, old.RateMilli, old.RateRevision, old.GroupIDs
		} else {
			n.PolicyVersion = 1
			n.RateMilli = 1000
			n.GroupIDs = []string{}
		}
	}
	if n.PolicyVersion != 1 || n.RateMilli < 0 || n.RateMilli > 100000 || n.RateMilli%10 != 0 {
		return errors.New("节点倍率需为 0–100，最多两位小数")
	}
	groups, err := s.nodeGroups()
	if err != nil {
		return err
	}
	scope := "private"
	if n.ManagedBy == publicManager {
		scope = "public"
	}
	known := map[string]bool{}
	for _, g := range groups {
		known[g.ID] = g.Scope == scope
	}
	seen := map[string]bool{}
	for _, id := range n.GroupIDs {
		if !known[id] || seen[id] {
			return errors.New("节点权限组不存在、重复或订阅范围不匹配")
		}
		seen[id] = true
	}
	sort.Strings(n.GroupIDs)
	if old == nil || n.RateMilli != old.RateMilli {
		n.RateRevision = randomToken(12)
	} else {
		n.RateRevision = old.RateRevision
	}
	return nil
}

func rateLabel(n Node) string { return fmt.Sprintf("%g×", float64(nodeRate(n))/1000) }
