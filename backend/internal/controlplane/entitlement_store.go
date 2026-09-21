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
const defaultSubsiteGroup = "default-subsite"
const defaultAdminPlan = "plan-admin"
const defaultDemoPlan = "plan-demo"

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
	NodeIDs     []string `json:"node_ids"`
	VLESS       bool     `json:"vless"`
	HY2         bool     `json:"hy2"`
	// System plans are seeded by the installation and retained as stable
	// entry points for the administrator and the demo experience.
	System bool   `json:"system,omitempty"`
	Kind   string `json:"kind,omitempty"`
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
	if err != nil {
		return err
	}
	if done == "1" {
		return s.initSubsiteGroup()
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
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.initSubsiteGroup()
}

// Seed once on both installation and upgrade. Never overwrite a renamed or
// disabled group, recreate an intentionally deleted one, or widen user access.
func (s *Store) initSubsiteGroup() error {
	done, err := s.readMeta("subsite_group_v1")
	if err != nil {
		return err
	}
	if done != "1" {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		g := NodeGroup{ID: defaultSubsiteGroup, Name: "默认子站节点组", Description: "挂载子站节点后，将本组分配给用户或套餐即可使用。", Scope: "subsite", Enabled: true, Sort: 2, Revision: "1"}
		b, err := json.Marshal(g)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO node_groups(id,doc) VALUES(?,?) ON CONFLICT(id) DO NOTHING", g.ID, b); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES('subsite_group_v1','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	if err = s.ensureDefaultSubsiteScope(); err != nil {
		return err
	}
	return s.repairMountedGroupScopes()
}

// default-subsite was shipped with a private scope by an older migration. It
// is a reserved system group, so correct only its scope while preserving any
// administrator rename, description, enabled state, and sort order.
func (s *Store) ensureDefaultSubsiteScope() error {
	groups, err := s.nodeGroups()
	if err != nil {
		return err
	}
	for _, g := range groups {
		if g.ID != defaultSubsiteGroup || g.Scope == "subsite" {
			continue
		}
		g.Scope = "subsite"
		g.Revision = randomToken(12)
		b, err := json.Marshal(g)
		if err != nil {
			return err
		}
		if _, err = s.db.Exec("UPDATE node_groups SET doc=? WHERE id=?", b, g.ID); err != nil {
			return err
		}
	}
	return nil
}

// Mounted nodes used to accept private/public groups. On upgrade, move those
// memberships into the dedicated default child group so a local entitlement
// can no longer authorize a direct child endpoint. The migration is idempotent
// and leaves an intentionally ungrouped mount ungrouped.
func (s *Store) repairMountedGroupScopes() error {
	sites, err := s.businessSites()
	if err != nil {
		return err
	}
	groups, err := s.nodeGroups()
	if err != nil {
		return err
	}
	for i := range sites {
		v := sites[i]
		if v.Connection == nil || v.Mount == nil || v.Removed {
			continue
		}
		changed := false
		for j := range v.Mount.Nodes {
			before := append([]string{}, v.Mount.Nodes[j].GroupIDs...)
			filtered := mountedGroupIDs(before, groups)
			if len(filtered) == 0 && len(before) > 0 {
				filtered = []string{defaultSubsiteGroup}
			}
			if !sameStrings(before, filtered) {
				v.Mount.Nodes[j].GroupIDs = filtered
				changed = true
			}
		}
		if changed {
			if err = s.saveBusinessSite(v); err != nil {
				return err
			}
		}
	}
	return nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// initDefaultPlans is intentionally run from bootstrap rather than the
// entitlement migration. The migration must remain a no-op for existing user
// data and tests that exercise only the legacy group upgrade; bootstrap is the
// installation/upgrade boundary where the default catalog belongs.
func (s *Store) initDefaultPlans() error {
	groups, err := s.nodeGroups()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, g := range groups {
		known[g.ID] = true
	}
	if !known[legacyPrivateGroup] || !known[legacyPublicGroup] || !known[defaultSubsiteGroup] {
		return errors.New("默认节点组尚未初始化")
	}
	plans := []Plan{
		{ID: defaultAdminPlan, Version: 1, Name: "管理员套餐", Description: "系统管理员完整管理权限。", Category: "系统", Notes: "系统预置套餐", Sort: 0, Quota: 0, ValidDays: 0, Cycle: "none", Timezone: "Asia/Shanghai", GroupIDs: []string{legacyPrivateGroup, legacyPublicGroup, defaultSubsiteGroup}, VLESS: true, HY2: true, System: true, Kind: "admin"},
		{ID: defaultDemoPlan, Version: 1, Name: "演示套餐", Description: "用于快速体验本站普通节点的演示套餐。", Category: "系统", Notes: "系统预置套餐", Sort: 1, Quota: 0, ValidDays: 30, Cycle: "30d", Timezone: "Asia/Shanghai", GroupIDs: []string{legacyPrivateGroup}, VLESS: true, HY2: true, Price: 0, System: true, Kind: "demo"},
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range plans {
		b, e := json.Marshal(p)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO plans(id,doc) VALUES(?,?) ON CONFLICT(id) DO NOTHING", p.ID, b); e != nil {
			return e
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.migrateUsersToPlans()
}

// Existing databases may contain accounts created before package-only access
// was introduced.  Give those accounts the zero-cost demo package once, so
// every account has one canonical entitlement and no independent mode remains.
func (s *Store) migrateUsersToPlans() error {
	demo, err := s.plan(defaultDemoPlan)
	if err != nil {
		return err
	}
	admin, err := s.plan(defaultAdminPlan)
	if err != nil {
		return err
	}
	users, err := s.records()
	if err != nil {
		return err
	}
	for i := range users {
		if users[i].Mount != nil || users[i].Entitlement != nil {
			continue
		}
		now := time.Now().Unix()
		p := demo
		if users[i].Role == "owner" {
			p = admin
		}
		assignPlan(&users[i], p, now)
		if err = s.save(&users[i]); err != nil {
			return err
		}
	}
	return nil
}

func userNodeGroupIDs(u User) []string {
	if u.Entitlement != nil {
		return u.Entitlement.GroupIDs
	}
	// Individual grants from older installations cannot override a plan.
	if u.NodeGroupIDs != nil {
		return *u.NodeGroupIDs
	}
	return []string{legacyPrivateGroup, legacyPublicGroup}
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
		ids := userNodeGroupIDs(r.User)
		for _, id := range ids {
			if enabled[id] {
				r.AllowedGroups = append(r.AllowedGroups, id)
			}
		}
	}
	return nil
}
func nodeGroupAllowed(r Record, n Node) bool {
	if r.Mount != nil {
		for _, id := range r.Mount.NodeIDs {
			if id == n.ID {
				return true
			}
		}
		return false
	}
	if r.Entitlement != nil {
		for _, id := range r.Entitlement.NodeIDs {
			if id == n.ID {
				return true
			}
		}
	}
	ids := r.AllowedGroups
	if !r.AccessResolved && r.CompiledGroups != nil {
		ids = *r.CompiledGroups
	} else if !r.AccessResolved {
		ids = userNodeGroupIDs(r.User)
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
	known := map[string]bool{}
	for _, g := range groups {
		known[g.ID] = true
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
