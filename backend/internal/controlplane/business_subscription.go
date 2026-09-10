package controlplane

import (
	"net/url"
	"strconv"
	"time"
)

// No network call occurs when serving a subscription. Only a site's acknowledged
// policy and scoped credentials enter the catalog; transport secrets stay local.
func (a *App) subscriptionCatalog(record Record, public bool, protocol string) ([]subscriptionEntry, error) {
	nodes, err := a.store.nodes()
	if err != nil {
		return nil, err
	}
	selected := []Node{}
	for _, n := range nodes {
		if (n.ManagedBy == publicManager) == public {
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
			entries = subscriptionEntries(a.cfg, record, selected, protocol)
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
			if (n.ManagedBy == publicManager) != public {
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
