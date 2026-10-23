package controlplane

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
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

// validateBusinessAllocations and validateBusinessUserQuota intentionally do
// not reject a configured site grant just because the sum of configured grants
// is larger than a member's remaining plan quota. Site grants are policy
// ceilings; the effective authorization is calculated when a site snapshot is
// issued. This lets administrators configure sites ahead of time while the
// runtime still stops issuing credentials as soon as the global plan quota is
// exhausted.
func (a *App) validateBusinessAllocations(_ BusinessSite) error {
	if _, err := a.store.businessSites(); err != nil {
		return err
	}
	_, err := a.store.records()
	return err
}

func (a *App) validateBusinessUserQuota(_ Record) error {
	if !a.cfg.controller() {
		return nil
	}
	_, err := a.store.businessSites()
	return err
}

// businessRuntimeReservation returns only quota that has actually been issued
// to a site and has not been acknowledged as settled. Configured grants are
// deliberately excluded: they may exceed a user's remaining plan quota and
// are capped when the next snapshot is generated.
func siteRuntimeReservation(v BusinessSite, user int64) int64 {
	if v.IssuedUnlimited[user] {
		return 0
	}
	return max(int64(0), v.Issued[user]-v.Usage[user].total())
}

func (a *App) businessRemainingQuota(user Record, siteID string) (int64, error) {
	if user.Quota == 0 {
		return 0, nil
	}
	remaining := max(int64(0), user.Quota-user.QuotaUsed())
	sites, err := a.store.businessSites()
	if err != nil {
		return 0, err
	}
	for _, site := range sites {
		if site.ID == siteID {
			continue
		}
		if site.IssuedUnlimited[user.ID] {
			return 0, nil
		}
		reserved := siteRuntimeReservation(site, user.ID)
		if reserved >= remaining {
			return 0, nil
		}
		remaining -= reserved
	}
	return remaining, nil
}

// effectiveBusinessGrant returns the quota that can be sent to a child for
// this sync. The configured grant remains unchanged; only the issued ceiling
// is reduced to the user's remaining global quota and any already issued
// reservations on other sites.
func (a *App) effectiveBusinessGrant(user Record, site BusinessSite, grant BusinessGrant) (BusinessGrant, bool, error) {
	if grant.UserID != user.ID {
		return grant, false, nil
	}
	used := site.Usage[user.ID].total()
	if grant.Quota > 0 && used >= grant.Quota {
		return grant, false, nil
	}
	if user.Quota == 0 {
		return grant, true, nil
	}
	remaining, err := a.businessRemainingQuota(user, site.ID)
	if err != nil {
		return grant, false, err
	}
	if remaining <= 0 {
		return grant, false, nil
	}
	maxQuota, err := domain.AddCounter(used, remaining)
	if err != nil {
		return grant, false, err
	}
	if grant.Quota == 0 || grant.Quota > maxQuota {
		grant.Quota = maxQuota
	}
	if grant.Quota <= used {
		return grant, false, nil
	}
	return grant, true, nil
}

// effectiveBusinessSnapshotGrant is used by pull-based business sites. The
// child keeps its consumed meter when a new snapshot is applied, so the quota
// sent here is a cumulative ceiling. The configured grant is capped by the
// site's usage plus the remaining global allowance.
func (a *App) effectiveBusinessSnapshotGrant(user Record, site BusinessSite, grant BusinessGrant) (BusinessGrant, bool, error) {
	if grant.UserID != user.ID {
		return grant, false, nil
	}
	used := site.Usage[user.ID].total()
	if grant.Quota > 0 && used >= grant.Quota {
		return grant, false, nil
	}
	if user.Quota == 0 {
		return grant, true, nil
	}
	remaining, err := a.businessRemainingQuota(user, site.ID)
	if err != nil {
		return grant, false, err
	}
	if remaining <= 0 {
		return grant, false, nil
	}
	maxQuota, err := domain.AddCounter(used, remaining)
	if err != nil {
		return grant, false, err
	}
	if grant.Quota == 0 || grant.Quota > maxQuota {
		grant.Quota = maxQuota
	}
	if grant.Quota <= used {
		return grant, false, nil
	}
	return grant, true, nil
}

// Core authorization accounts for quota already issued to other sites.
// Configured site grants are policy ceilings and do not reduce local access
// until a child actually receives an authorization.
func (a *App) coreRecords() ([]Record, error) {
	records, err := a.store.records()
	if err != nil {
		return nil, err
	}
	return a.coreRecordsFrom(records)
}

// coreRecordsFrom applies the core authorization policy to loaded records.
// Every step is per record, so a single record yields the same result it
// would have inside the full set.
func (a *App) coreRecordsFrom(records []Record) ([]Record, error) {
	var err error
	if err = a.filterMountRecords(records); err != nil {
		return nil, err
	}
	if a.store.meta("site_paused") == "true" {
		for i := range records {
			records[i].Enabled = false
		}
	}
	if !a.cfg.businessAgent() {
		if err = a.store.resolveAccess(records); err != nil {
			return nil, err
		}
	}
	if a.cfg.businessAgent() {
		expires := a.businessLeaseDeadline()
		managed := make([]Record, 0, len(records))
		for i := range records {
			// A managed child keeps a local owner for independent panel access,
			// but that account is outside the controller's membership policy. Do
			// not put it in the proxy core's managed authorization set; otherwise
			// local credentials can make the child configuration diverge from the
			// snapshot acknowledged by the controller.
			if records[i].Role != "user" {
				continue
			}
			if expires <= time.Now().Unix() {
				records[i].Enabled = false
			}
			managed = append(managed, records[i])
		}
		return managed, nil
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
			if s.IssuedUnlimited[records[i].ID] {
				records[i].Enabled = false
				reserved = records[i].Quota
				break
			}
			reserved += siteRuntimeReservation(s, records[i].ID)
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
	v.refreshMonthlyBudget(time.Now().Unix())
	if err := a.verifyBusinessMeter(v, in); err != nil {
		return err
	}
	a.store.runtimeMu.RLock()
	defer a.store.runtimeMu.RUnlock()
	history := a.store.logsEnabledLocked()
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	closed, err := a.reconcileClosedBusinessUsage(tx, v, in.NodeUsage)
	if err != nil {
		return err
	}
	// The closed traffic was generated before the current period started. Keep
	// the site's absolute reservation aligned with its global watermark so the
	// late sample cannot consume the new period's grant or trigger a false
	// budget exhaustion.
	for userID, delta := range closed {
		inc, e := domain.AddCounter(delta.quotaUpload, delta.quotaDownload)
		if e != nil || inc == 0 {
			if e != nil {
				return e
			}
			continue
		}
		for i := range v.Grants {
			if v.Grants[i].UserID == userID && v.Grants[i].Quota > 0 {
				v.Grants[i].Quota, e = domain.AddCounter(v.Grants[i].Quota, inc)
				if e != nil {
					return e
				}
			}
		}
		for i := range v.SentGrants {
			if v.SentGrants[i].UserID == userID && v.SentGrants[i].Quota > 0 {
				v.SentGrants[i].Quota, e = domain.AddCounter(v.SentGrants[i].Quota, inc)
				if e != nil {
					return e
				}
			}
		}
		if issued, ok := v.Issued[userID]; ok && issued > 0 {
			v.Issued[userID], e = domain.AddCounter(issued, inc)
			if e != nil {
				return e
			}
		}
	}
	for id, next := range in.Usage {
		if id <= 0 || next.Upload < 0 || next.Download < 0 || next.VLESS < 0 || next.HY2 < 0 || next.Upload > 1<<60 || next.Download > 1<<60 || next.VLESS > 1<<60 || next.HY2 > 1<<60 || next.rawTotal() != next.VLESS+next.HY2 {
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
		if next.Metered && (next.QuotaUpload < 0 || next.QuotaDownload < 0 || next.QuotaUpload > 1<<60 || next.QuotaDownload > 1<<60) {
			return errors.New("invalid weighted traffic report")
		}
		if next.Metered && previous.Metered && (next.QuotaUpload < previous.QuotaUpload || next.QuotaDownload < previous.QuotaDownload) {
			return errors.New("weighted watermark moved backwards")
		}
		if next.Upload < previous.Upload || next.Download < previous.Download || next.VLESS < previous.VLESS || next.HY2 < previous.HY2 {
			return errors.New("business counters moved backwards; restore requires reconciliation")
		}
		du, dd, dv, dh := next.Upload-previous.Upload, next.Download-previous.Download, next.VLESS-previous.VLESS, next.HY2-previous.HY2
		if du > 0 || dd > 0 {
			var e error
			v.MonthlyUsage, e = domain.AddCounter(v.MonthlyUsage, du)
			if e == nil {
				v.MonthlyUsage, e = domain.AddCounter(v.MonthlyUsage, dd)
			}
			if e != nil {
				return e
			}
			v.markBudgetExhausted(time.Now().Unix())
		}
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
		u.InitMeter("legacy-"+businessUsageKey(id), time.Now().Unix())
		qu, qd := du, dd
		if next.Metered {
			pu, pd := previous.Upload, previous.Download
			if previous.Metered {
				pu, pd = previous.QuotaUpload, previous.QuotaDownload
			}
			qu, qd = next.QuotaUpload-pu, next.QuotaDownload-pd
		}
		if u.Meter.Upload, err = domain.AddCounter(u.Meter.Upload, qu); err != nil {
			return err
		}
		if u.Meter.Download, err = domain.AddCounter(u.Meter.Download, qd); err != nil {
			return err
		}
		// The live meter stores a global weighted watermark. Rebase the current
		// period by the portion that belongs to the already closed period, so
		// historical late traffic is retained without charging the new period.
		if late := closed[id]; late.quotaUpload > 0 || late.quotaDownload > 0 {
			u.Meter.BaseUpload, err = domain.AddCounter(u.Meter.BaseUpload, late.quotaUpload)
			if err != nil {
				return err
			}
			u.Meter.BaseDownload, err = domain.AddCounter(u.Meter.BaseDownload, late.quotaDownload)
			if err != nil {
				return err
			}
			u.Meter.RawBaseUpload, err = domain.AddCounter(u.Meter.RawBaseUpload, late.upload)
			if err != nil {
				return err
			}
			u.Meter.RawBaseDownload, err = domain.AddCounter(u.Meter.RawBaseDownload, late.download)
			if err != nil {
				return err
			}
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
	for _, row := range in.NodeUsage {
		if _, err = tx.Exec("INSERT INTO business_node_usage(site_id,user_id,node_id,period_id,rate_revision,rate_milli,upload,download) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(site_id,user_id,node_id,period_id,rate_revision) DO UPDATE SET upload=excluded.upload,download=excluded.download", v.ID, row.UserID, row.NodeID, row.PeriodID, row.RateRevision, row.RateMilli, row.Upload, row.Download); err != nil {
			return err
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
