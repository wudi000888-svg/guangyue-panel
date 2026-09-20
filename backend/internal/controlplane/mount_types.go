package controlplane

import (
	"encoding/json"
	"sort"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
)

const mountLeaseSeconds = 300

type MountedNode struct {
	NodeID   string   `json:"node_id"`
	Name     string   `json:"name"`
	Enabled  bool     `json:"enabled"`
	GroupIDs []string `json:"group_ids"`
}
type MountCatalog struct {
	Info     BusinessInfo `json:"info"`
	Nodes    []Node       `json:"nodes"`
	Groups   []NodeGroup  `json:"groups"`
	Revision string       `json:"revision"`
	Paused   bool         `json:"paused"`
}
type MountAccount struct {
	UserID      int64       `json:"user_id"`
	NodeIDs     []string    `json:"node_ids"`
	Generation  string      `json:"generation"`
	Credentials Credentials `json:"credentials"`
}
type SiteMount struct {
	Assignment string         `json:"assignment,omitempty"` // "groups" opts into automatic main-site membership/budgets; empty is legacy manual.
	UsageAck   []NodeUsage    `json:"usage_ack,omitempty"`
	Catalog    MountCatalog   `json:"catalog"`
	Nodes      []MountedNode  `json:"nodes"`
	Sequence   int64          `json:"sequence"`
	LeaseUntil int64          `json:"lease_until"`
	LastSync   int64          `json:"last_sync"`
	Error      string         `json:"error"`
	Accounts   []MountAccount `json:"accounts,omitempty"`
}
type MountUser struct {
	PeriodID   string   `json:"period_id"`
	UserID     int64    `json:"user_id"`
	NodeIDs    []string `json:"node_ids"`
	Generation string   `json:"generation"`
	Quota      int64    `json:"quota"`
	Expires    int64    `json:"expires"`
	VLESS      bool     `json:"vless"`
	HY2        bool     `json:"hy2"`
}
type MountSync struct {
	UsageFloor      map[int64]BusinessUsage `json:"usage_floor,omitempty"`
	UsageAck        []NodeUsage             `json:"usage_ack,omitempty"`
	MasterID        string                  `json:"master_id"`
	Sequence        int64                   `json:"sequence"`
	CatalogRevision string                  `json:"catalog_revision"`
	Users           []MountUser             `json:"users"`
}
type MountResult struct {
	Sequence   int64                   `json:"sequence"`
	LeaseUntil int64                   `json:"lease_until"`
	Accounts   []MountAccount          `json:"accounts"`
	Usage      map[int64]BusinessUsage `json:"usage"`
	NodeUsage  []NodeUsage             `json:"node_usage"`
}
type MountSharing struct {
	Enabled  bool     `json:"enabled"`
	All      bool     `json:"all"`
	NodeIDs  []string `json:"node_ids"`
	Revision string   `json:"revision"`
}
type mountBinding struct {
	MasterID   string `json:"master_id"`
	Sequence   int64  `json:"sequence"`
	Hash       string `json:"hash"`
	LeaseUntil int64  `json:"lease_until"`
}

func (s *Store) mountSharing(id string) (MountSharing, error) {
	v := MountSharing{Enabled: true, All: true, NodeIDs: []string{}, Revision: "initial"}
	raw, err := s.readMeta("mount-sharing:" + id)
	if err == nil && raw != "" {
		err = json.Unmarshal([]byte(raw), &v)
	}
	return v, err
}
func (s MountSharing) allows(id string) bool {
	if !s.Enabled {
		return false
	}
	if s.All {
		return true
	}
	for _, n := range s.NodeIDs {
		if n == id {
			return true
		}
	}
	return false
}
func mountGeneration(r Record) string { return digest("mounted-user/" + r.Credentials.HY2) }
func mountNodePolicy(m MountedNode, source Node) Node {
	source.GroupIDs = append([]string{}, m.GroupIDs...)
	source.Enabled = source.Enabled && m.Enabled
	if m.Name != "" {
		source.Name = m.Name
	}
	return source
}

// Mounted nodes belong to the master site's sub-site groups. Keeping a local
// group on a mounted node makes it eligible for the normal subscription too.
func subsiteGroupIDs(ids []string, groups []NodeGroup) []string {
	allowed := map[string]bool{}
	for _, g := range groups {
		if g.Scope == "subsite" {
			allowed[g.ID] = true
		}
	}
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if allowed[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Repair configurations written before mounted nodes were restricted to
// sub-site groups. The caller persists the containing site and resynchronizes
// authorizations when this returns true.
func normalizeMountedNodeGroups(nodes []MountedNode, groups []NodeGroup) bool {
	changed := false
	for i := range nodes {
		filtered := subsiteGroupIDs(nodes[i].GroupIDs, groups)
		if len(filtered) != len(nodes[i].GroupIDs) {
			changed = true
		} else {
			for j, id := range filtered {
				if nodes[i].GroupIDs[j] != id {
					changed = true
					break
				}
			}
		}
		nodes[i].GroupIDs = filtered
	}
	return changed
}
func mountSource(c MountCatalog, id string) (Node, bool) {
	for _, n := range c.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}
func cloneMountAccess(v *domain.MountAccess) *domain.MountAccess {
	if v == nil {
		return nil
	}
	c := *v
	c.NodeIDs = append([]string{}, v.NodeIDs...)
	return &c
}
