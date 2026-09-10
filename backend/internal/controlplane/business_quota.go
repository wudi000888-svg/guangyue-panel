package controlplane

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func siteReservation(v BusinessSite, user int64) int64 {
	limit := v.Issued[user]
	for _, g := range v.Grants {
		if g.UserID == user && g.Quota > limit {
			limit = g.Quota
		}
	}
	return max(int64(0), limit-v.Usage[user].total())
}
func (a *App) validateBusinessAllocations(candidate BusinessSite) error {
	sites, err := a.store.businessSites()
	if err != nil {
		return err
	}
	found := false
	for i := range sites {
		if sites[i].ID == candidate.ID {
			sites[i] = candidate
			found = true
		}
	}
	if !found {
		sites = append(sites, candidate)
	}
	users, err := a.store.records()
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Quota == 0 {
			continue
		}
		remaining := max(int64(0), u.Quota-u.Upload-u.Download)
		for _, s := range sites {
			if s.IssuedUnlimited[u.ID] {
				return errBusinessQuota
			}
			for _, g := range s.Grants {
				if g.UserID == u.ID && g.Quota == 0 {
					return errBusinessQuota
				}
			}
			reserved := siteReservation(s, u.ID)
			if reserved > remaining {
				return errBusinessQuota
			}
			remaining -= reserved
		}
	}
	return nil
}
func (a *App) validateBusinessUserQuota(u Record) error {
	if !a.cfg.controller() || u.Quota == 0 {
		return nil
	}
	sites, err := a.store.businessSites()
	if err != nil {
		return err
	}
	remaining := max(int64(0), u.Quota-u.Upload-u.Download)
	for _, s := range sites {
		if s.IssuedUnlimited[u.ID] {
			return errBusinessQuota
		}
		for _, g := range s.Grants {
			if g.UserID == u.ID && g.Quota == 0 {
				return errBusinessQuota
			}
		}
		reserved := siteReservation(s, u.ID)
		if reserved > remaining {
			return errBusinessQuota
		}
		remaining -= reserved
	}
	return nil
}

// Core authorization accounts for unconsumed allocations elsewhere. Subscription
// authorization still uses the user's global record and the target site's grant.
func (a *App) coreRecords() ([]Record, error) {
	records, err := a.store.records()
	if err != nil {
		return nil, err
	}
	if a.cfg.businessAgent() {
		expires := a.businessLeaseDeadline()
		for i := range records {
			if expires <= time.Now().Unix() {
				records[i].Enabled = false
			}
		}
		return records, nil
	}
	if !a.cfg.controller() {
		return records, nil
	}
	sites, err := a.store.businessSites()
	if err != nil {
		return nil, err
	}
	for i := range records {
		if records[i].Quota == 0 {
			continue
		}
		reserved := int64(0)
		for _, s := range sites {
			reserved += siteReservation(s, records[i].ID)
		}
		if reserved >= records[i].Quota {
			records[i].Enabled = false
			continue
		}
		records[i].Quota -= reserved
	}
	return records, nil
}

// Persist counters and the acknowledged allocation in one transaction. Replayed
// or out-of-order totals never lower a watermark or count the same bytes twice.
func (a *App) acceptBusinessUsage(v *BusinessSite, in BusinessHeartbeat) error {
	a.store.runtimeMu.RLock()
	defer a.store.runtimeMu.RUnlock()
	history := a.store.logsEnabledLocked()
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, next := range in.Usage {
		if id <= 0 || next.Upload < 0 || next.Download < 0 || next.VLESS < 0 || next.HY2 < 0 || next.Upload > 1<<60 || next.Download > 1<<60 || next.VLESS > 1<<60 || next.HY2 > 1<<60 || next.total() != next.VLESS+next.HY2 {
			return errors.New("invalid traffic report")
		}
		previous, known := v.Usage[id]
		authorized := known
		for _, g := range v.SentGrants {
			if g.UserID == id {
				authorized = true
			}
		}
		if _, ok := v.Issued[id]; ok {
			authorized = true
		}
		if !authorized {
			return errors.New("traffic user is not assigned to this site")
		}
		if next.Upload < previous.Upload || next.Download < previous.Download || next.VLESS < previous.VLESS || next.HY2 < previous.HY2 {
			return errors.New("business counters moved backwards; restore requires reconciliation")
		}
		du, dd, dv, dh := next.Upload-previous.Upload, next.Download-previous.Download, next.VLESS-previous.VLESS, next.HY2-previous.HY2
		if du == 0 && dd == 0 {
			v.Usage[id] = next
			continue
		}
		var b []byte
		if err = tx.QueryRow("SELECT doc FROM users WHERE id=?", id).Scan(&b); err != nil {
			// Deleted users remain acknowledged without recreating their identities.
			if errors.Is(err, sql.ErrNoRows) {
				v.Usage[id] = next
				continue
			}
			return err
		}
		var u User
		if err = json.Unmarshal(b, &u); err != nil {
			return err
		}
		if u.Upload > 1<<60-du || u.Download > 1<<60-dd {
			return errors.New("traffic counter limit exceeded")
		}
		u.Upload += du
		u.Download += dd
		u.VLESSTraffic += dv
		u.HY2Traffic += dh
		b, err = json.Marshal(u)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE users SET doc=? WHERE id=?", b, id); err != nil {
			return err
		}
		if history {
			if _, err = tx.Exec("INSERT INTO traffic(hour,user_id,upload,download) VALUES(?,?,?,?) ON CONFLICT(hour,user_id) DO UPDATE SET upload=traffic.upload+excluded.upload,download=traffic.download+excluded.download", time.Now().Unix()/3600*3600, id, du, dd); err != nil {
				return err
			}
		}
		v.Usage[id] = next
	}
	if in.Applied != "" && in.Applied == v.Desired {
		// Never release an allocation on a partial report. Even zero-use and
		// retired identities must provide their final cumulative watermark.
		required := map[int64]bool{}
		for id := range v.Issued {
			required[id] = true
		}
		for id := range v.IssuedUnlimited {
			required[id] = true
		}
		for id := range v.Usage {
			required[id] = true
		}
		for _, g := range v.SentGrants {
			required[g.UserID] = true
		}
		for id := range required {
			if _, ok := in.Usage[id]; !ok {
				return errors.New("incomplete acknowledged traffic report")
			}
		}
		v.IssuedNodes = append([]Node{}, v.SentNodes...)
		v.Applied = in.Applied
		v.Issued = map[int64]int64{}
		v.IssuedUnlimited = map[int64]bool{}
		for _, g := range v.SentGrants {
			v.Issued[g.UserID] = g.Quota
			if g.Quota == 0 {
				v.IssuedUnlimited[g.UserID] = true
			}
		}
	}
	b, err := a.store.vault.seal(v)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE business_sites SET doc=? WHERE id=?", b, v.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func businessUsageKey(id int64) string { return fmt.Sprintf("%d", id) }
