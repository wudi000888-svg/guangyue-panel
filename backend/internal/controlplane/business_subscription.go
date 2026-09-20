package controlplane

import (
	"net/url"
	"strconv"
	"time"
)

// No network call occurs when serving a subscription. Only a site's acknowledged
// policy and scoped credentials enter the catalog; transport secrets stay local.
func (a *App) subscriptionCatalog(record Record, public bool, protocol string) ([]subscriptionEntry, error) {
	source := "legacy-private"
	if public {
		source = "public"
	}
	return a.subscriptionCatalogSource(record, source, protocol)
}
func (a *App) subscriptionCatalogSource(record Record, source, protocol string) ([]subscriptionEntry, error) {
	resolved := []Record{record}
	if err := a.store.resolveAccess(resolved); err != nil {
		return nil, err
	}
	record = resolved[0]
	nodes, err := a.store.nodes()
	if err != nil {
		return nil, err
	}
	groups, err := a.store.nodeGroups()
	if err != nil {
		return nil, err
	}
	subsiteGroups := map[string]bool{defaultSubsiteGroup: true}
	for _, g := range groups {
		if g.Scope == "subsite" {
			subsiteGroups[g.ID] = true
		}
	}
	isSubsiteNode := func(n Node) bool {
		for _, id := range normalizeNodePolicy(n).GroupIDs {
			if subsiteGroups[id] {
				return true
			}
		}
		return false
	}
	selected := []Node{}
	for _, n := range nodes {
		local := n.ManagedBy != publicManager && !isSubsiteNode(n)
		if source == "all" || source == "mixed" || source == "local" && local || source == "private" && local || source == "legacy-private" && n.ManagedBy != publicManager || source == "public" && n.ManagedBy == publicManager {
			selected = append(selected, n)
		}
	}
	entries := []subscriptionEntry{}
	local, err := a.coreRecords()
	if err != nil {
		return nil, err
	}
	for _, u := range local {
		if u.ID == record.ID && u.Active() {
			entries = subscriptionEntries(a.cfg, u, selected, protocol)
			break
		}
	}
	if !a.cfg.controller() || !record.Active() {
		return entries, nil
	}
	sites, err := a.store.businessSites()
	if err != nil {
		return nil, err
	}
	for _, site := range sites {
		if site.Connection != nil {
			if source == "all" || source == "mixed" || source == "mounted" || source == "subsite" {
				entries = append(entries, a.mountedSubscriptionEntries(record, site, protocol)...)
			}
			continue
		}
		if source == "mounted" || source == "local" || source == "private" {
			continue
		}
		if !site.Enabled || site.Info == nil || site.Applied == "" || site.Applied != site.Desired || site.LeaseUntil <= time.Now().Unix() || !a.businessOwnerEnabled(site) {
			continue
		}
		var grant *BusinessGrant
		for i := range site.Grants {
			if site.Grants[i].UserID == record.ID {
				grant = &site.Grants[i]
				break
			}
		}
		if grant == nil {
			continue
		}
		sent := false
		for _, g := range site.SentGrants {
			if g.UserID == record.ID {
				sent = true
				break
			}
		}
		if !sent {
			continue
		}
		if grant.Quota > 0 && site.Usage[record.ID].total() >= grant.Quota {
			continue
		}
		remote := businessRecord(record, site.ID, site.SentNodes)
		selected = nil
		for _, n := range site.SentNodes {
			if source != "all" && source != "mixed" && (n.ManagedBy == publicManager) != (source == "public") {
				continue
			}
			for _, reported := range site.Reports {
				if n.ID == reported.ID {
					n.ProbeIP = reported.ProbeIP
					n.Country = reported.Country
					n.CountryCode = reported.CountryCode
					n.Quality = reported.Quality
					n.CheckedAt = reported.CheckedAt
					n.ProbedAt = reported.ProbedAt
					if reported.Name != "" {
						n.Name = reported.Name
					}
					break
				}
			}
			n.Name += " · " + site.Name
			selected = append(selected, n)
		}
		for _, entry := range subscriptionEntries(businessConfig(*site.Info), remote, selected, protocol) {
			entry.siteID = site.ID
			entry.siteName = site.Name
			entry.node.ID = site.ID + "/" + entry.node.ID
			entries = append(entries, entry)
		}
	}
	// Stable duplicate suffixes make the combined Mihomo proxy list unambiguous.
	used := map[string]int{}
	for i := range entries {
		base := entries[i].name
		used[base]++
		if used[base] > 1 {
			entries[i].name = base + " · " + strconv.Itoa(used[base])
			entries[i].proxy["name"] = entries[i].name
			if u, e := url.Parse(entries[i].uri); e == nil {
				u.Fragment = entries[i].name
				entries[i].uri = u.String()
			}
		}
	}
	return entries, nil
}

func (a *App) mountedSubscriptionEntries(record Record, site BusinessSite, protocol string) []subscriptionEntry {
	m := site.Mount
	if m == nil || !site.Enabled || site.Removed || m.Catalog.Paused || m.LeaseUntil <= time.Now().Unix() || !a.businessOwnerEnabled(site) {
		return nil
	}
	granted := false
	for _, g := range site.Grants {
		if g.UserID == record.ID && (g.Quota == 0 || site.Usage[record.ID].total() < g.Quota) {
			granted = true
		}
	}
	if !granted {
		return nil
	}
	for _, account := range m.Accounts {
		if account.UserID != record.ID || account.Generation != mountGeneration(record) {
			continue
		}
		remote := record
		remote.Credentials = account.Credentials
		nodes := []Node{}
		for _, mount := range m.Nodes {
			n, ok := mountSource(m.Catalog, mount.NodeID)
			if !ok || !mount.Enabled || !n.Enabled {
				continue
			}
			allowed := false
			for _, id := range account.NodeIDs {
				allowed = allowed || id == n.ID
			}
			if !allowed {
				continue
			}
			n = mountNodePolicy(mount, n)
			n.Name = "[挂载·" + site.Name + "] " + n.Name
			nodes = append(nodes, n)
		}
		out := subscriptionEntries(businessConfig(m.Catalog.Info), remote, nodes, protocol)
		for i := range out {
			out[i].siteID = site.ID
			out[i].siteName = site.Name
			out[i].mounted = true
			out[i].node.ID = site.ID + "/" + out[i].node.ID
		}
		return out
	}
	return nil
}
