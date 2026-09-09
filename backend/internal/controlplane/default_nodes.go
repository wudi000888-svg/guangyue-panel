package controlplane

import (
	"errors"
	"strings"
)

const defaultDirectMessage = "默认直连节点必须保留并启用；绑定 IP 池出口请新增节点"

// Adopt existing direct nodes without changing their IDs, subscriptions or SNI.
// Older installations may already use *-main for a proxy exit; keep that route
// intact and choose another direct node, or create a new one when needed.
func (s *Store) ensureDefaultDirectNodes() error {
	nodes, err := s.nodes()
	if err != nil {
		return err
	}
	records, err := s.records()
	if err != nil {
		return err
	}
	chosen := map[string]string{}
	for _, protocol := range []string{"vless", "hy2"} {
		best, rank := -1, 0
		for i, n := range nodes {
			if n.Protocol != protocol || n.ManagedBy != "" || n.Exit != "direct" || n.ExitID != "" {
				continue
			}
			score := 1
			if n.Enabled {
				score += 2
			}
			if n.ID == protocol+"-main" {
				score++
			}
			if n.DefaultDirect {
				score += 8
			}
			if score > rank {
				best, rank = i, score
			}
		}
		if best < 0 {
			id := protocol + "-default-direct"
			for _, n := range nodes {
				if n.ID == id {
					id += "-" + digest(randomToken(8))[:8]
					break
				}
			}
			nodes = append(nodes, Node{ID: id, Name: "待检测 · " + strings.ToUpper(protocol), Protocol: protocol, Exit: "direct"})
			best = len(nodes) - 1
		}
		nodes[best].DefaultDirect, nodes[best].Enabled = true, true
		chosen[protocol] = nodes[best].ID
	}
	for i := range nodes {
		nodes[i].DefaultDirect = nodes[i].ID == chosen[nodes[i].Protocol]
	}
	for i := range records {
		// Clone maps so a failed transaction never mutates a caller's snapshot.
		credentials := map[string]string{}
		for id, key := range records[i].Credentials.VLESS {
			credentials[id] = key
		}
		if credentials[chosen["vless"]] == "" {
			credentials[chosen["vless"]] = uuid()
		}
		records[i].Credentials.VLESS = credentials
	}
	return s.saveInfrastructure(nil, nodes, records, nil, nil)
}

func validateDefaultDirectEdit(old, next Node) error {
	if old.DefaultDirect && (!next.Enabled || next.Protocol != old.Protocol || next.ExitID != "" || next.Exit != "" && next.Exit != "direct") {
		return errors.New(defaultDirectMessage)
	}
	return nil
}
