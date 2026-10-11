package controlplane

import (
	"net/http"
	"sort"
)

// NodeSquarePlan is the deliberately small package summary shown in the
// member-facing node directory. It contains catalog information only; the
// subscription credentials and internal policy remain server-side.
type NodeSquarePlan struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Price     int64  `json:"price,string"`
	ValidDays int    `json:"valid_days"`
	Cycle     string `json:"cycle"`
}

// NodeSquareNode is a public directory view. Keep this DTO separate from
// Node: Node carries Reality, proxy credentials, exit addresses and bridge
// secrets that must never cross the member API boundary.
type NodeSquareNode struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Protocol    string           `json:"protocol"`
	Enabled     bool             `json:"enabled"`
	Country     string           `json:"country,omitempty"`
	CountryCode string           `json:"country_code,omitempty"`
	Plans       []NodeSquarePlan `json:"plans"`
}

func nodeSquarePlanAllows(p Plan, n Node) bool {
	n = normalizeNodePolicy(n)
	if p.ProtocolEnabledFor(n.Protocol) == false {
		return false
	}
	selectedGroup := false
	for _, group := range p.GroupIDs {
		for _, nodeGroup := range n.GroupIDs {
			if group == nodeGroup {
				selectedGroup = true
				break
			}
		}
		if selectedGroup {
			break
		}
	}
	if !selectedGroup {
		return false
	}
	if len(p.NodeIDs) == 0 {
		return true
	}
	key := planNodeKey(n)
	for _, id := range p.NodeIDs {
		if id == key || id == n.ID {
			// Plain IDs are accepted for local nodes for the same reason they
			// are accepted by the entitlement resolver: old selections predate
			// site-qualified node keys. A qualified key is always preferred.
			if id == n.ID && n.AccessKey != "" {
				continue
			}
			return true
		}
	}
	return false
}

// ProtocolEnabledFor keeps protocol filtering in one place for the directory
// and avoids listing a HY2 node in a VLESS-only offer (and vice versa).
func (p Plan) ProtocolEnabledFor(protocol string) bool {
	switch protocol {
	case "vless":
		return p.VLESS
	case "hy2":
		return p.HY2
	default:
		return false
	}
}

func (a *App) nodeSquare(w http.ResponseWriter, _ *http.Request, _ Record) {
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	offers, err := readDocuments[Offer](a.store, "commerce_offers")
	if err != nil {
		failure(w, 500, "读取套餐失败")
		return
	}
	plans := make([]struct {
		offer Offer
		plan  Plan
	}, 0, len(offers))
	for _, offer := range offers {
		if !offer.Enabled || offer.Plan.ID == "" {
			continue
		}
		plan, e := a.store.plan(offer.Plan.ID)
		if e != nil || plan.Archived {
			continue
		}
		plans = append(plans, struct {
			offer Offer
			plan  Plan
		}{offer: offer, plan: plan})
	}
	out := make([]NodeSquareNode, 0, len(nodes))
	for _, n := range nodes {
		if !n.SquareVisible || !n.Enabled {
			continue
		}
		entries := make([]NodeSquarePlan, 0)
		for _, candidate := range plans {
			if !nodeSquarePlanAllows(candidate.plan, n) {
				continue
			}
			entries = append(entries, NodeSquarePlan{ID: candidate.plan.ID, Name: candidate.plan.Name, Price: candidate.offer.Price, ValidDays: candidate.plan.ValidDays, Cycle: candidate.plan.Cycle})
		}
		// A directory entry without an on-sale package cannot be used by a
		// member, so it is intentionally omitted from the public catalog.
		if len(entries) == 0 {
			continue
		}
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].Name == entries[j].Name {
				return entries[i].ID < entries[j].ID
			}
			return entries[i].Name < entries[j].Name
		})
		out = append(out, NodeSquareNode{ID: n.ID, Name: n.Name, Protocol: n.Protocol, Enabled: true, Country: n.Country, CountryCode: n.CountryCode, Plans: entries})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	jsonResponse(w, 200, object{"nodes": out})
}
