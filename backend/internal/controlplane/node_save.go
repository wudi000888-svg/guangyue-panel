package controlplane

import (
	"net/http"
	"regexp"
)

var nodeSaveRequestID = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,80}$`)

func validNodeSaveRequestID(id string) bool { return nodeSaveRequestID.MatchString(id) }

// Creation is idempotent for the lifetime of the node, including after edits
// and process restarts. The fingerprint binds the original input to its actor.
// Never reapply a saved node: doing so could interrupt the retrying connection.
func replayNodeCreate(w http.ResponseWriter, nodes []Node, input Node, hash string) bool {
	if input.ID != "" || input.SaveRequestID == "" {
		return false
	}
	for _, n := range nodes {
		if n.CreateRequestID != input.SaveRequestID {
			continue
		}
		if n.CreateRequestHash != hash {
			failure(w, 409, "该保存请求已用于其他节点配置，请刷新列表后重试")
		} else {
			jsonResponse(w, 200, n.public())
		}
		return true
	}
	return false
}
