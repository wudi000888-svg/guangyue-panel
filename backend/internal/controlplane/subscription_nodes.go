package controlplane

// A link is paired with its authorized node and sanitized report at render time.
type SubscriptionNode struct {
	Mounted  bool     `json:"mounted"`
	Source   string   `json:"source"`
	GroupIDs []string `json:"group_ids"`
	MemberNodeQuality
	SiteID   string   `json:"site_id,omitempty"`
	SiteName string   `json:"site_name,omitempty"`
	DNS      *NodeDNS `json:"dns,omitempty"`
	URI      string   `json:"uri"`
}

func authorizedNodeGroups(r Record, n Node) []string {
	out := []string{}
	ids := userNodeGroupIDs(r.User)
	for _, g := range n.GroupIDs {
		for _, id := range ids {
			if g == id {
				out = append(out, g)
				break
			}
		}
	}
	return out
}
