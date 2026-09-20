package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/httpapi"
)

// createSubsiteEgress turns one shared child node into a normal master-side
// subscription egress. Client traffic enters a master node, reaches the
// local bridge, and the bridge connects to the child node. This is separate
// from a mounted node, whose client connects directly to the child endpoint.
func (a *App) createSubsiteEgress(ctx context.Context, site BusinessSite, nodeID, label string) (IPResource, error) {
	if site.Connection == nil || site.Mount == nil || site.Connection.Scope != "manage" {
		return IPResource{}, errors.New("子站未启用节点中转")
	}
	n, ok := mountSource(site.Mount.Catalog, nodeID)
	if !ok || !n.Enabled {
		return IPResource{}, errors.New("子站出口节点不存在或已停用")
	}
	body, _ := json.Marshal(object{"node_id": nodeID})
	response, err := a.callSubsite(ctx, *site.Connection, httpapi.GatewayRequest{Method: "POST", Path: "/api/node-pool/egress", Body: body})
	if err != nil {
		return IPResource{}, err
	}
	var remote struct {
		NodeID      string `json:"node_id"`
		Name        string `json:"name"`
		Protocol    string `json:"protocol"`
		ProbeIP     string `json:"probe_ip"`
		Country     string `json:"country"`
		CountryCode string `json:"country_code"`
		Proxy       object `json:"proxy"`
	}
	if response.Status != 200 || json.Unmarshal(response.Body, &remote) != nil || remote.NodeID != nodeID || remote.Proxy == nil || remote.Protocol != "vless" && remote.Protocol != "hy2" {
		return IPResource{}, errors.New("子站未返回有效的中转出口")
	}
	typ, _ := remote.Proxy["type"].(string)
	if typ == "" {
		return IPResource{}, errors.New("子站中转协议无效")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	pools, err := a.store.pools()
	if err != nil {
		return IPResource{}, err
	}
	id := "subsite-" + digest(site.ID + "/" + nodeID)[:12]
	port := 21000
	used := map[int]bool{}
	for _, p := range pools {
		if p.ID == id && p.Exit == "subscription" && p.Source == site.ID && p.Enabled {
			p.Label = label
			if p.Label == "" {
				p.Label = "子站 · " + site.Name + " · " + remote.Name
			}
			p.Revision = randomToken(12)
			p.ProbeIP, p.Country, p.CountryCode = remote.ProbeIP, remote.Country, remote.CountryCode
			p.Reachable = remote.ProbeIP != ""
			if err = a.store.savePoolNodes([]IPResource{p}, nil); err != nil {
				return IPResource{}, err
			}
			return p, a.ensureBridge()
		}
		if p.Exit == "subscription" {
			used[p.BridgePort] = true
		}
	}
	for used[port] && port <= 21255 {
		port++
	}
	if port > 21255 || len(pools) >= 256 {
		return IPResource{}, errors.New("IP 池已达到上限")
	}
	if label == "" {
		label = "子站 · " + site.Name + " · " + remote.Name
	}
	p := IPResource{Node: Node{ID: id, Name: label, Protocol: remote.Protocol, Enabled: true, Exit: "subscription", Host: site.Mount.Catalog.Info.VLESSHost, Port: 443, ProbeIP: remote.ProbeIP, Country: remote.Country, CountryCode: remote.CountryCode, Upstream: remote.Proxy, UpstreamType: typ, BridgePort: port, BridgePassword: randomToken(24)}, PoolGroup: "subsite", Source: site.ID, Label: label, Notes: "主站中转，子站 IP 出口 · " + nodeID, Revision: randomToken(12), Reachable: remote.ProbeIP != ""}
	if remote.Protocol == "hy2" {
		p.Host = site.Mount.Catalog.Info.HY2Host
	}
	if err = a.validateBridge(append(append([]IPResource{}, pools...), p)); err != nil {
		return IPResource{}, err
	}
	if err = a.store.savePoolNodes([]IPResource{p}, nil); err != nil {
		return IPResource{}, err
	}
	if err = a.ensureBridge(); err != nil {
		_ = a.store.savePoolState(nil, nil, []string{p.ID})
		_ = a.ensureBridge()
		return IPResource{}, err
	}
	return p, nil
}

func (a *App) subsiteEgressAPI(w http.ResponseWriter, r *http.Request, actor Record, siteID string) {
	if r.Method != "POST" {
		failure(w, 405, "方法不支持")
		return
	}
	var in struct {
		NodeID string `json:"node_id"`
		Label  string `json:"label"`
	}
	if !decodeBusiness(w, r, &in) || in.NodeID == "" {
		return
	}
	a.mu.Lock()
	site, err := a.store.businessSite(siteID)
	a.mu.Unlock()
	if err != nil || site.Removed {
		failure(w, 404, "子站不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	p, err := a.createSubsiteEgress(ctx, site, in.NodeID, in.Label)
	if err != nil {
		failure(w, 502, err.Error())
		return
	}
	a.store.audit(actor.Username, "create-subsite-egress", siteID+"/"+in.NodeID)
	jsonResponse(w, 201, p.public(nil))
}
