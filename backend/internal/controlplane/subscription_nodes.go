package controlplane

// A link is paired with its authorized node and sanitized report at render time.
type SubscriptionNode struct {
	MemberNodeQuality
	SiteID   string   `json:"site_id,omitempty"`
	SiteName string   `json:"site_name,omitempty"`
	DNS      *NodeDNS `json:"dns,omitempty"`
	URI      string   `json:"uri"`
}
