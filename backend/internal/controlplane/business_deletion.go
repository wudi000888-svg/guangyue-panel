package controlplane

// Keep the issued configuration and accounting watermarks until acknowledgement,
// but remove desired node membership with the deleted exit in the same commit.
func pruneBusinessNodes(sites []BusinessSite, removePools []string) ([]BusinessSite, []BusinessSite) {
	removed := map[string]bool{}
	for _, id := range removePools {
		removed[id] = true
	}
	old, next := []BusinessSite{}, []BusinessSite{}
	for _, s := range sites {
		nodes := []Node{}
		for _, n := range s.Nodes {
			if !removed[n.ExitID] {
				nodes = append(nodes, n)
			}
		}
		if len(nodes) == len(s.Nodes) {
			continue
		}
		old = append(old, s)
		s.Nodes = nodes
		s.Revision = randomToken(12)
		next = append(next, s)
	}
	return old, next
}
