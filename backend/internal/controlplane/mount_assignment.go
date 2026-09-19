package controlplane

import (
	"errors"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
)

func mountedPolicyNodes(v BusinessSite) []Node {
	nodes := []Node{}
	if v.Mount == nil || !v.Enabled || v.Removed || v.Mount.Catalog.Paused {
		return nodes
	}
	sources := map[string]Node{}
	for _, n := range v.Mount.Catalog.Nodes {
		sources[n.ID] = n
	}
	for _, m := range v.Mount.Nodes {
		if n, ok := sources[m.NodeID]; ok && m.Enabled && n.Enabled {
			nodes = append(nodes, mountNodePolicy(m, n))
		}
	}
	return nodes
}
func mayUseMountNodes(u Record, nodes []Node) bool {
	if u.Mount != nil || !u.Active() {
		return false
	}
	for _, n := range nodes {
		if n.Enabled && nodeGroupAllowed(u, n) && (n.Protocol == "vless" && u.VLESS || n.Protocol == "hy2" && u.HY2) {
			return true
		}
	}
	return false
}

// Called under App.mu. Grants are derived from the main database, not from
// child-side users. Only unused, settled capacity can move between sites.
// Issued reservations survive shrinking, withdrawal and offline children until
// the existing acknowledgement transaction accepts the final watermarks.
func (a *App) refreshMountGrants(v *BusinessSite) error {
	if v.Mount == nil || v.Mount.Assignment != "groups" {
		return nil
	}
	users, err := a.store.records()
	if err != nil {
		return err
	}
	if err = a.store.resolveAccess(users); err != nil {
		return err
	}
	sites, err := a.store.businessSites()
	if err != nil {
		return err
	}
	local, err := a.store.nodes()
	if err != nil {
		return err
	}
	candidateNodes := []Node{}
	if a.businessOwnerEnabled(*v) {
		candidateNodes = mountedPolicyNodes(*v)
	}
	otherNodes := map[string][]Node{}
	for _, s := range sites {
		if s.ID != v.ID && s.Mount != nil && s.Mount.Assignment == "groups" && a.businessOwnerEnabled(s) {
			otherNodes[s.ID] = mountedPolicyNodes(s)
		}
	}
	previous := map[int64]BusinessGrant{}
	for _, g := range v.Grants {
		previous[g.UserID] = g
	}
	grants := []BusinessGrant{}
	for _, u := range users {
		if !mayUseMountNodes(u, candidateNodes) {
			continue
		}
		if u.Meter == nil {
			// Bootstrap owners from older installations may not have sent
			// traffic or received a plan yet. Keep their legacy usage intact.
			u.InitMeter("legacy-"+businessUsageKey(u.ID), u.Created)
			if err = a.store.save(&u); err != nil {
				return err
			}
		}
		g := BusinessGrant{UserID: u.ID, PeriodID: u.Meter.PeriodID}
		if u.Quota > 0 {
			remaining := max(int64(0), u.Quota-u.QuotaUsed())
			available, shares := remaining, int64(1)
			for _, s := range sites {
				if s.ID == v.ID {
					continue
				}
				available = max(int64(0), available-siteReservation(s, u.ID))
				// An unacknowledged unlimited grant cannot be replaced by a
				// finite one while its final usage is still unknown.
				if s.IssuedUnlimited[u.ID] {
					available = 0
				}
				if mayUseMountNodes(u, otherNodes[s.ID]) {
					shares++
				}
			}
			if mayUseMountNodes(u, local) {
				shares++
			}
			if v.IssuedUnlimited[u.ID] || available == 0 {
				continue
			}
			budget := min(available, max(int64(1), remaining/shares))
			old := previous[u.ID]
			unused := max(int64(0), old.Quota-v.Usage[u.ID].total())
			// Refill below half the target. Stable limits avoid restarting
			// proxy configuration for every small traffic report.
			if old.PeriodID == g.PeriodID && unused > 0 && unused <= budget && unused >= max(int64(1), budget/2) {
				budget = unused
			}
			g.Budget = budget
			g.Quota, err = domain.AddCounter(v.Usage[u.ID].total(), budget)
			if err != nil {
				return err
			}
		}
		grants = append(grants, g)
	}
	if len(grants) > 256 || len(v.Mount.Nodes)*len(grants) > 4096 {
		return errors.New("每站最多挂载 64 个节点、256 个用户，节点与用户组合不超过 4096")
	}
	v.Grants = grants
	return nil
}
