package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Sharing is a local owner decision, never a gateway route. A remote manager
// cannot broaden its own export allowlist or re-enable a blocked identity.
func (a *App) mountSharingAPI(w http.ResponseWriter, r *http.Request, actor Record, id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.Contains(id, "/") || !a.validMountToken(id) {
		failure(w, 404, "管理令牌不存在或已失效")
		return
	}
	share, err := a.store.mountSharing(id)
	if err != nil {
		failure(w, 500, "读取共享设置失败")
		return
	}
	nodes, err := a.store.nodes()
	if err != nil {
		failure(w, 500, "读取节点失败")
		return
	}
	if r.Method == "PUT" {
		var in MountSharing
		if !decode(w, r, &in) {
			return
		}
		if in.Revision != share.Revision {
			failure(w, 409, "共享设置已变化，请刷新")
			return
		}
		known := map[string]bool{}
		for _, n := range nodes {
			known[n.ID] = true
		}
		seen := map[string]bool{}
		for _, id := range in.NodeIDs {
			if !known[id] || seen[id] {
				failure(w, 400, "共享节点不存在或重复")
				return
			}
			seen[id] = true
		}
		in.Revision = randomToken(12)
		b, _ := json.Marshal(in)
		if err = a.store.setMeta("mount-sharing:"+id, string(b)); err != nil {
			failure(w, 500, "保存共享设置失败")
			return
		}
		share = in
		a.status = "pending"
		a.store.audit(actor.Username, "mount-sharing", id)
		if err = a.reconcile(); err == nil {
			err = a.settleHYRevocations()
		}
	} else if r.Method != "GET" {
		failure(w, 405, "方法不支持")
		return
	}
	safe := []Node{}
	for _, n := range nodes {
		v := memberNodeState(n)
		v.GroupIDs = n.GroupIDs
		safe = append(safe, v)
	}
	jsonResponse(w, 200, object{"sharing": share, "nodes": safe, "pending": err != nil})
}
