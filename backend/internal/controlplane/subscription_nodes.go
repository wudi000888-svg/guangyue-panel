package controlplane

// A link is paired with its authorized node and sanitized report at render time.
type SubscriptionNode struct {
	MemberNodeQuality
	DNS *NodeDNS `json:"dns,omitempty"`
	URI string   `json:"uri"`
}
