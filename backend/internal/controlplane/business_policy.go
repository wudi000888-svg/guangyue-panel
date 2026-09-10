package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func businessDefaultNodes() []Node {
	return []Node{{ID: "vless-main", Name: "本机直连", Protocol: "vless", Exit: "direct", Enabled: true, DefaultDirect: true, DNS: defaultNodeDNS()}, {ID: "hy2-main", Name: "本机直连", Protocol: "hy2", Exit: "direct", Enabled: true, DefaultDirect: true, DNS: defaultNodeDNS()}}
}
func (a *App) materializeBusinessNodes(v BusinessSite) ([]Node, error) {
	out := businessDefaultNodes()
	for i := range out {
		out[i] = normalizeNodePolicy(out[i])
		for _, policy := range v.DefaultNodes {
			if policy.ID == out[i].ID {
				out[i].PolicyVersion = policy.PolicyVersion
				out[i].RateMilli = policy.RateMilli
				out[i].RateRevision = policy.RateRevision
				out[i].GroupIDs = policy.GroupIDs
			}
		}
	}
	for _, template := range v.Nodes {
		p, err := a.store.pool(template.ExitID)
		if err != nil || !p.Enabled {
			continue
		} // Source deletion removes its managed bindings on the next pull.
		n := template
		n = bindPool(n, p)
		// Exit health and quality are measured from the business site, not inherited
		// from a different entry path on the controller.
		n.Quality = nil
		n.Speed = nil
		n.ProbeIP = ""
		n.Country = ""
		n.CountryCode = ""
		n.CheckedAt = 0
		n.ProbedAt = 0
		n.ProbeError = ""
		if p.PoolGroup == "public" {
			n.ManagedBy = publicManager
		} else {
			n.ManagedBy = "business"
		}
		n.BridgePort = 0
		n.BridgePassword = ""

		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (a *App) businessSnapshot(v *BusinessSite) (BusinessSnapshot, error) {
	nodes, err := a.materializeBusinessNodes(*v)
	if err != nil {
		return BusinessSnapshot{}, err
	}
	out := BusinessSnapshot{SiteID: v.ID, Nodes: nodes, Users: []BusinessAccount{}, IssuedAt: time.Now().Unix(), LeaseSeconds: businessLeaseSeconds, RuntimeMode: a.store.runtimeSnapshot().Mode}
	users, err := a.store.records()
	if err != nil {
		return out, err
	}
	if err = a.store.resolveAccess(users); err != nil {
		return out, err
	}
	grants := []BusinessGrant{}
	for _, r := range users {
		if !v.Enabled || !a.businessOwnerEnabled(*v) || !r.Active() {
			continue
		}
		for _, g := range v.Grants {
			if g.UserID != r.ID {
				continue
			}
			used := v.Usage[r.ID]
			if g.Quota > 0 && used.total() >= g.Quota {
				continue
			}
			if v.Info != nil && v.Info.Protocol < 2 && r.Entitlement != nil {
				continue
			}
			r = businessRecord(r, v.ID, nodes)
			r.InitMeter("legacy-"+businessUsageKey(r.ID), r.Created)
			compiled := append([]string{}, r.AllowedGroups...)
			r.CompiledGroups = &compiled
			if r.Meter != nil {
				m := *r.Meter
				m.Upload = 0
				m.Download = 0
				m.BaseUpload = 0
				m.BaseDownload = 0
				m.RawBaseUpload = 0
				m.RawBaseDownload = 0
				m.UploadRemainder = 0
				m.DownloadRemainder = 0
				m.InitialUpload = 0
				m.InitialDownload = 0
				r.Meter = &m
				g.PeriodID = m.PeriodID
				v.PeriodRules[businessUsageKey(r.ID)+"/"+m.PeriodID] = true
			}
			if v.Info != nil && v.Info.Protocol < 2 {
				r.Meter = nil
				r.CompiledGroups = nil
			}
			r.Quota = g.Quota
			r.Upload = 0
			r.Download = 0
			r.VLESSTraffic = 0
			r.HY2Traffic = 0
			out.Users = append(out.Users, BusinessAccount{User: r.User, Credentials: r.Credentials})
			grants = append(grants, g)
			break
		}
	}
	if v.Enabled && a.businessOwnerEnabled(*v) {
		for _, cmd := range v.Commands {
			if cmd.State == "queued" || cmd.State == "running" {
				out.Commands = append(out.Commands, cmd)
			}
		}
	}
	out.Revision = businessSnapshotRevision(out)
	v.Desired = out.Revision
	v.SentGrants = grants
	v.SentNodes = nodes
	for _, n := range nodes {
		n = normalizeNodePolicy(n)
		v.RateRules[n.ID+"/"+n.RateRevision] = n.RateMilli
	}
	v.LeaseUntil = out.IssuedAt + out.LeaseSeconds
	for _, g := range grants {
		if g.Quota == 0 {
			v.IssuedUnlimited[g.UserID] = true
		} else if g.Quota > v.Issued[g.UserID] {
			v.Issued[g.UserID] = g.Quota
		}
	}
	if err = a.store.saveBusinessSite(*v); err != nil {
		return out, err
	}
	return out, nil
}
func (a *App) validateBusinessPolicy(v BusinessSite) error {
	if len(v.Nodes) > 14 || len(v.Grants) > 256 {
		return errors.New("每站点最多 14 个附加节点和 256 位成员")
	}
	users, err := a.store.records()
	if err != nil {
		return err
	}
	known := map[int64]bool{}
	for _, u := range users {
		known[u.ID] = true
	}
	seen := map[int64]bool{}
	for _, g := range v.Grants {
		if !known[g.UserID] || seen[g.UserID] || g.Quota < 0 || g.Quota > 1<<60 {
			return errors.New("业务站成员或额度无效")
		}
		seen[g.UserID] = true
	}
	ids := map[string]bool{}
	for _, n := range v.Nodes {
		if n.ID == "" || len(n.ID) > 48 || !simpleID(n.ID) || ids[n.ID] || n.ID == "vless-main" || n.ID == "hy2-main" || n.ExitID == "" || n.Protocol != "vless" && n.Protocol != "hy2" {
			return errors.New("业务节点配置无效")
		}
		ids[n.ID] = true
		p, e := a.store.pool(n.ExitID)
		if e != nil || !p.Enabled {
			return errors.New("分配的出口不存在或已停用")
		}
		if n.RealitySNI != "" && !validHostname(n.RealitySNI) {
			return errors.New("伪装域名无效")
		}
		if n.DNS != nil {
			if err := validateNodeDNS(n.DNS); err != nil {
				return err
			}
		}
	}
	others, err := a.store.businessSites()
	if err != nil {
		return err
	}
	exits := map[string]bool{}
	for _, n := range v.Nodes {
		exits[n.ExitID] = true
	}
	for _, s := range others {
		if s.ID == v.ID {
			continue
		}
		if !v.Exclusive && !s.Exclusive {
			continue
		}
		for _, n := range append(append([]Node{}, s.Nodes...), append(s.SentNodes, s.IssuedNodes...)...) {
			if exits[n.ExitID] {
				return fmt.Errorf("出口已由业务站 %s 占用，请先解除分配并等待确认", s.Name)
			}
		}
	}
	// An exclusive grant also excludes bindings on the controller's local core.
	if v.Exclusive {
		local, e := a.store.nodes()
		if e != nil {
			return e
		}
		for _, n := range local {
			if exits[n.ExitID] {
				return errors.New("独占出口仍绑定主控本机节点，请先解除绑定")
			}
		}
	}
	if v.Info != nil && v.Info.Protocol < 2 {
		ns, e := a.materializeBusinessNodes(v)
		if e != nil {
			return e
		}
		for _, n := range ns {
			n = normalizeNodePolicy(n)
			expected := legacyPrivateGroup
			if n.ManagedBy == publicManager {
				expected = legacyPublicGroup
			}
			if n.RateMilli != 1000 || len(n.GroupIDs) != 1 || n.GroupIDs[0] != expected {
				return errors.New("旧业务站不支持节点倍率和权限组，请先升级业务站")
			}
		}
		for _, u := range users {
			for _, g := range v.Grants {
				if g.UserID == u.ID && u.Entitlement != nil {
					return errors.New("旧业务站不支持套餐权限，请先升级业务站")
				}
			}
		}
	}
	return a.validateBusinessAllocations(v)
}
func simpleID(s string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return s != ""
}
func businessNodeTemplate(n Node) Node {
	return Node{PolicyVersion: n.PolicyVersion, RateMilli: n.RateMilli, RateRevision: n.RateRevision, GroupIDs: n.GroupIDs, ID: n.ID, Protocol: n.Protocol, ExitID: n.ExitID, Enabled: n.Enabled, RealitySNI: strings.TrimSpace(n.RealitySNI), DNS: n.DNS, Name: n.Name}
}
func (a *App) checkExclusiveBusinessExit(exitID string) error {
	if !a.cfg.controller() || exitID == "" {
		return nil
	}
	sites, e := a.store.businessSites()
	if e != nil {
		return e
	}
	for _, s := range sites {
		if s.Exclusive {
			for _, n := range append(append([]Node{}, s.Nodes...), append(s.SentNodes, s.IssuedNodes...)...) {
				if n.ExitID == exitID {
					return errors.New("该出口已独占分配给业务站")
				}
			}
		}
	}
	return nil
}
func equalBusinessInfo(a, b BusinessInfo) bool {
	a.Version = ""
	a.Protocol = 0
	b.Protocol = 0
	b.Version = ""
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
