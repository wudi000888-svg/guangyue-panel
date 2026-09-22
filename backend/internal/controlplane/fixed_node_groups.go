package controlplane

import (
	"encoding/json"
	"strings"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
)

func localNodeGroup(n Node) string {
	if n.ManagedBy == publicManager {
		return legacyPublicGroup
	}
	return legacyPrivateGroup
}

func fixedGroupIDs(ids []string) []string {
	out := []string{}
	for _, id := range []string{legacyPrivateGroup, defaultSubsiteGroup, legacyPublicGroup} {
		for _, selected := range ids {
			if selected == id {
				out = append(out, id)
				break
			}
		}
	}
	return out
}

// Remove the retired custom-group model at the upgrade boundary. Never turn
// a custom group's limited membership into access to an entire source group.
// Affected offers are taken off sale until their package is reconfigured.
func (s *Store) initFixedNodeGroups() error {
	done, err := s.readMeta("fixed_node_groups_v1")
	if err != nil || done == "1" {
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
	plans, err := s.plans()
	if err != nil {
		return err
	}
	users, err := s.records()
	if err != nil {
		return err
	}
	offers, err := readDocuments[Offer](s, "commerce_offers")
	if err != nil {
		return err
	}
	rawSettings, err := s.readMeta("public_settings")
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM node_groups"); err != nil {
		return err
	}
	groups := []NodeGroup{
		{ID: legacyPrivateGroup, Name: "默认本地节点组", Scope: "private", Description: "本地节点自动归入本组，使用权限由套餐决定。", Enabled: true, Sort: 0, Revision: "fixed-1"},
		{ID: defaultSubsiteGroup, Name: "默认子站节点组", Scope: "subsite", Description: "挂载子站节点自动归入本组，流量直接经过子站。", Enabled: true, Sort: 1, Revision: "fixed-1"},
		{ID: legacyPublicGroup, Name: "默认公共节点组", Scope: "public", Description: "公共节点自动归入本组，使用权限由套餐决定。", Enabled: true, Sort: 2, Revision: "fixed-1"},
	}
	for _, g := range groups {
		if _, err = tx.Exec("INSERT INTO node_groups(id,doc) VALUES(?,?)", g.ID, jsonBytes(g)); err != nil {
			return err
		}
	}
	for _, n := range nodes {
		n.GroupIDs = []string{localNodeGroup(n)}
		b, e := s.vault.seal(n)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("UPDATE nodes SET doc=? WHERE id=?", b, n.ID); err != nil {
			return err
		}
	}
	for _, site := range sites {
		if site.Mount != nil {
			normalizeMountedNodeGroups(site.Mount.Nodes, groups)
			site.Mount.Assignment = "groups"
			site.Mount.Accounts = nil
			site.Mount.LeaseUntil = 0
			site.Mount.Error = "等待授权或撤销确认"
		}
		for i := range site.DefaultNodes {
			site.DefaultNodes[i].GroupIDs = []string{defaultSubsiteGroup}
		}
		for i := range site.Nodes {
			site.Nodes[i].GroupIDs = []string{defaultSubsiteGroup}
		}
		site.Revision = randomToken(12)
		b, e := s.vault.seal(site)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("UPDATE business_sites SET doc=? WHERE id=?", b, site.ID); err != nil {
			return err
		}
	}
	changedPlans := map[string]bool{}
	for _, p := range plans {
		ids := fixedGroupIDs(p.GroupIDs)
		if len(ids) == len(p.GroupIDs) {
			continue
		}
		changedPlans[p.ID] = true
		p.GroupIDs = ids
		if len(ids) == 0 {
			p.NodeIDs = []string{}
		}
		if _, err = tx.Exec("UPDATE plans SET doc=? WHERE id=?", jsonBytes(p), p.ID); err != nil {
			return err
		}
	}
	clean := func(e *domain.Entitlement) {
		if e == nil {
			return
		}
		e.GroupIDs = fixedGroupIDs(e.GroupIDs)
		if len(e.GroupIDs) == 0 {
			e.NodeIDs = []string{}
		}
	}
	for _, u := range users {
		clean(u.Entitlement)
		for i := range u.PlanQueue {
			clean(u.PlanQueue[i].Entitlement)
		}
		if u.Entitlement != nil {
			u.NodeGroupIDs = nil
		}
		if _, err = tx.Exec("UPDATE users SET doc=? WHERE id=?", jsonBytes(u.User), u.ID); err != nil {
			return err
		}
	}
	for _, o := range offers {
		if changedPlans[o.Plan.ID] || len(fixedGroupIDs(o.Plan.GroupIDs)) != len(o.Plan.GroupIDs) {
			o.Enabled = false
			o.Version++
			if _, err = tx.Exec("UPDATE commerce_offers SET version=?,enabled=0,doc=? WHERE id=?", o.Version, jsonBytes(o), o.ID); err != nil {
				return err
			}
		}
	}
	settings := defaultPublicSettings()
	if rawSettings != "" {
		if err = json.Unmarshal([]byte(rawSettings), &settings); err != nil {
			return err
		}
		settings.NodeGroupIDs = []string{legacyPublicGroup}
		if _, err = tx.Exec("UPDATE meta SET value=? WHERE key='public_settings'", string(jsonBytes(settings))); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES('fixed_node_groups_v1','1')"); err != nil {
		return err
	}
	return tx.Commit()
}

// A site-qualified key prevents identically named child and local nodes from
// granting access to one another. An empty selection for a group means all.
func planNodeKey(n Node) string {
	if n.AccessKey != "" {
		return n.AccessKey
	}
	return localNodeGroup(n) + "/" + n.ID
}

func planSelectsNode(e *domain.Entitlement, n Node, group string) bool {
	if e == nil {
		return true
	}
	filtered := false
	for _, id := range e.NodeIDs {
		if strings.HasPrefix(id, group+"/") {
			filtered = true
			if id == planNodeKey(n) {
				return true
			}
		} else if !strings.Contains(id, "/") && group != defaultSubsiteGroup {
			// Old single-node choices can only name this site's nodes.
			filtered = true
			if n.AccessKey == "" && id == n.ID {
				return true
			}
		}
	}
	return !filtered
}
