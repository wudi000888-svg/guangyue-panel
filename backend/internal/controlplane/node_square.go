package controlplane

import (
	"net/http"
	"sort"
	"strings"
)

// NodeSquarePlan is a catalog-only package summary. Credentials, proxy
// addresses and entitlement state never cross the node-square API boundary.
type NodeSquarePlan struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Price     int64  `json:"price,string"`
	ValidDays int    `json:"valid_days"`
	Cycle     string `json:"cycle"`
}

// NodeSquareNode is shared by owner and member catalogs. Owners see every
// configured node so hidden, disabled, stale and unpriced rows remain
// controllable. Members only receive rows whose Published flag is true.
type NodeSquareNode struct {
	ID            string           `json:"id"`
	SiteID        string           `json:"site_id"`
	SiteName      string           `json:"site_name"`
	Name          string           `json:"name"`
	Protocol      string           `json:"protocol"`
	Enabled       bool             `json:"enabled"`
	SquareVisible bool             `json:"square_visible"`
	Published     bool             `json:"published"`
	Country       string           `json:"country,omitempty"`
	CountryCode   string           `json:"country_code,omitempty"`
	Plans         []NodeSquarePlan `json:"plans"`
}

type nodeSquareOffer struct { offer Offer; plan Plan }

func nodeSquarePlanAllows(p Plan, n Node) bool {
	n = normalizeNodePolicy(n)
	if !p.ProtocolEnabledFor(n.Protocol) { return false }
	selectedGroup := false
	for _, group := range p.GroupIDs {
		for _, nodeGroup := range n.GroupIDs {
			if group == nodeGroup { selectedGroup = true; break }
		}
		if selectedGroup { break }
	}
	if !selectedGroup { return false }
	if len(p.NodeIDs) == 0 { return true }
	key := planNodeKey(n)
	for _, id := range p.NodeIDs {
		if id == key || n.AccessKey == "" && id == n.ID { return true }
	}
	return false
}

func (p Plan) ProtocolEnabledFor(protocol string) bool {
	switch protocol { case "vless": return p.VLESS; case "hy2": return p.HY2; default: return false }
}

func (a *App) nodeSquareOffers() ([]nodeSquareOffer, error) {
	offers, err := readDocuments[Offer](a.store, "commerce_offers")
	if err != nil { return nil, err }
	out := make([]nodeSquareOffer, 0, len(offers))
	for _, offer := range offers {
		if !offer.Enabled || offer.Plan.ID == "" { continue }
		plan, e := a.store.plan(offer.Plan.ID)
		if e != nil || plan.Archived || plan.ID == "" { continue }
		out = append(out, nodeSquareOffer{offer: offer, plan: plan})
	}
	return out, nil
}

func nodeSquarePlans(offers []nodeSquareOffer, n Node) []NodeSquarePlan {
	entries := make([]NodeSquarePlan, 0)
	for _, candidate := range offers {
		if !nodeSquarePlanAllows(candidate.plan, n) { continue }
		entries = append(entries, NodeSquarePlan{ID: candidate.plan.ID, Name: candidate.plan.Name, Price: candidate.offer.Price, ValidDays: candidate.plan.ValidDays, Cycle: candidate.plan.Cycle})
	}
	sort.SliceStable(entries, func(i, j int) bool { if entries[i].Name == entries[j].Name { return entries[i].ID < entries[j].ID }; return entries[i].Name < entries[j].Name })
	return entries
}

func nodeSquareSort(nodes []NodeSquareNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].SiteName != nodes[j].SiteName { return nodes[i].SiteName < nodes[j].SiteName }
		if nodes[i].Name != nodes[j].Name { return nodes[i].Name < nodes[j].Name }
		if nodes[i].SiteID != nodes[j].SiteID { return nodes[i].SiteID < nodes[j].SiteID }
		return nodes[i].ID < nodes[j].ID
	})
}

func (a *App) localNodeSquareRows(offers []nodeSquareOffer) ([]NodeSquareNode, error) {
	nodes, err := a.store.nodes(); if err != nil { return nil, err }
	siteID, siteName := a.cfg.siteID(), a.store.siteSettings().PanelName
	if siteName == "" { siteName = siteID }
	out := make([]NodeSquareNode, 0, len(nodes))
	for _, n := range nodes {
		plans := nodeSquarePlans(offers, n)
		out = append(out, NodeSquareNode{ID:n.ID, SiteID:siteID, SiteName:siteName, Name:n.Name, Protocol:n.Protocol, Enabled:n.Enabled, SquareVisible:n.SquareVisible, Published:n.Enabled && n.SquareVisible && len(plans)>0, Country:n.Country, CountryCode:n.CountryCode, Plans:plans})
	}
	return out, nil
}

func (a *App) mountedNodeSquareRows(offers []nodeSquareOffer) ([]NodeSquareNode, error) {
	sites, err := a.store.businessSites(); if err != nil { return nil, err }
	out := []NodeSquareNode{}
	for _, site := range sites {
		if site.Connection == nil || site.Mount == nil || site.Removed { continue }
		for _, mounted := range site.Mount.Nodes {
			source, ok := mountSource(site.Mount.Catalog, mounted.NodeID)
			if !ok { source = Node{ID:mounted.NodeID, Name:mounted.Name} }
			n := mountNodePolicy(site.ID, mounted, source)
			plans := nodeSquarePlans(offers, n)
			enabled := site.Enabled && !site.Mount.Catalog.Paused && source.Enabled && mounted.Enabled
			out = append(out, NodeSquareNode{ID:mounted.NodeID, SiteID:site.ID, SiteName:site.Name, Name:n.Name, Protocol:source.Protocol, Enabled:enabled, SquareVisible:mounted.SquareVisible, Published:enabled && mounted.SquareVisible && len(plans)>0, Country:source.Country, CountryCode:source.CountryCode, Plans:plans})
		}
	}
	return out, nil
}

func (a *App) nodeSquareRows() ([]NodeSquareNode, error) {
	offers, err := a.nodeSquareOffers(); if err != nil { return nil, err }
	local, err := a.localNodeSquareRows(offers); if err != nil { return nil, err }
	mounted, err := a.mountedNodeSquareRows(offers); if err != nil { return nil, err }
	rows := append(local, mounted...); nodeSquareSort(rows); return rows, nil
}

func (a *App) nodeSquare(w http.ResponseWriter, r *http.Request, actor Record) {
	if r.Method == "PUT" { a.nodeSquareUpdate(w, r, actor); return }
	if r.Method != "GET" { failure(w, 405, "方法不支持"); return }
	rows, err := a.nodeSquareRows()
	if err != nil { failure(w, 500, "读取节点广场失败"); return }
	if actor.Role != "owner" {
		published := rows[:0]
		for _, row := range rows { if row.Published { published = append(published, row) } }
		rows = published
	}
	jsonResponse(w, 200, object{"nodes": rows})
}

func (a *App) nodeSquareUpdate(w http.ResponseWriter, r *http.Request, actor Record) {
	if actor.Role != "owner" { failure(w, 403, "需要管理员权限"); return }
	var in struct { NodeID string `json:"node_id"`; SiteID string `json:"site_id"`; SquareVisible bool `json:"square_visible"` }
	if !decode(w, r, &in) { return }
	in.NodeID, in.SiteID = strings.TrimSpace(in.NodeID), strings.TrimSpace(in.SiteID)
	if in.NodeID == "" || in.SiteID == "" || len(in.NodeID)>128 || len(in.SiteID)>128 { failure(w, 400, "节点或站点无效"); return }
	if in.SiteID == a.cfg.siteID() {
		a.mu.Lock()
		defer a.mu.Unlock()
		nodes, err := a.store.nodes(); if err != nil { failure(w, 500, "读取节点失败"); return }
		for _, n := range nodes {
			if n.ID != in.NodeID { continue }
			n.SquareVisible = in.SquareVisible
			if err = a.store.saveNode(n); err != nil { failure(w, 500, "保存节点展示设置失败"); return }
			a.store.audit(actor.Username, "node-square", in.SiteID+"/"+in.NodeID)
			jsonResponse(w, 200, object{"ok":true,"node_id":in.NodeID,"site_id":in.SiteID,"square_visible":in.SquareVisible}); return
		}
		failure(w, 404, "节点不存在"); return
	}
	a.mu.Lock(); defer a.mu.Unlock()
	site, err := a.store.businessSite(in.SiteID)
	if err != nil || site.Connection == nil || site.Mount == nil { failure(w, 404, "站点或节点不存在"); return }
	if site.Connection.Scope != "manage" { failure(w, 403, "站点连接不允许管理"); return }
	for i := range site.Mount.Nodes {
		if site.Mount.Nodes[i].NodeID != in.NodeID { continue }
		site.Mount.Nodes[i].SquareVisible = in.SquareVisible
		site.Mount.SquareRevision = randomToken(12)
		if err = a.store.saveBusinessSite(site); err != nil { failure(w, 500, "保存节点展示设置失败"); return }
		a.store.audit(actor.Username, "node-square", in.SiteID+"/"+in.NodeID)
		jsonResponse(w, 200, object{"ok":true,"node_id":in.NodeID,"site_id":in.SiteID,"square_visible":in.SquareVisible,"revision":site.Mount.SquareRevision}); return
	}
	failure(w, 404, "节点不存在")
}
