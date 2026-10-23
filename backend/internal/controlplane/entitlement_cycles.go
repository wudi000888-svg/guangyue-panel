package controlplane

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
)

func (a *App) validateEntitlementSites(u Record, action string) error {
	if !a.cfg.controller() {
		return nil
	}
	sites, err := a.store.businessSites()
	if err != nil {
		return err
	}
	for _, s := range sites {
		assigned := false
		for _, g := range s.Grants {
			assigned = assigned || g.UserID == u.ID
		}
		_, issued := s.Issued[u.ID]
		if s.Connection == nil && (assigned || issued || s.IssuedUnlimited[u.ID]) && (s.Info == nil || s.Info.Protocol < 2) && (u.Entitlement != nil || u.NodeGroupIDs != nil || action == "reset") {
			return fmt.Errorf("请先升级业务站 %s，旧版本不支持套餐权限和配额周期", s.Name)
		}
	}
	return nil
}

// A reset first stops the user everywhere. Only an acknowledged revocation with
// final lifetime watermarks releases old grants. Offline sites keep it pending.
func (a *App) advanceQuotaPeriods(now int64) error {
	if a.cfg.businessAgent() {
		return nil
	}
	users, err := a.store.records()
	if err != nil {
		return err
	}
	sites, err := a.store.businessSites()
	if err != nil {
		return err
	}
	pending, err := a.store.commerceBarrierUsers()
	if err != nil {
		return err
	}
	type dueUser struct {
		id      int64
		restore bool
	}
	ready := []dueUser{}
	for _, u := range users {
		if pending[u.ID] {
			continue
		}
		// A package purchase pauses the previous package.  Once the active
		// package expires, revoke its downstream grants and restore the most
		// recent paused slot before ordinary quota-cycle processing runs.
		if len(u.PlanQueue) > 0 && u.Expires > 0 && now >= u.Expires {
			if u.Meter == nil {
				u.InitMeter("period-"+randomToken(12), now)
			}
			if !u.Meter.PendingReset {
				u.Meter.PendingReset = true
				if err = a.store.save(&u); err != nil {
					return err
				}
				continue
			}
			if !grantsSettled(sites, u.ID) {
				continue
			}
			ready = append(ready, dueUser{u.ID, true})
			continue
		}
		if u.Meter == nil {
			continue
		}
		m := u.Meter
		if !m.PendingReset && m.End > 0 && now >= m.End {
			m.PendingReset = true
			if err = a.store.save(&u); err != nil {
				return err
			}
		}
		if !m.PendingReset || !grantsSettled(sites, u.ID) {
			continue
		}
		ready = append(ready, dueUser{u.ID, false})
	}
	if len(ready) == 0 {
		return nil
	}
	// Every due user is already stopped (PendingReset). One reconcile, one
	// final traffic read and one HY2 revocation cover all of them; doing this
	// per user restarted HY2 once per user at the start of each month.
	if err = a.reconcile(); err != nil {
		return err
	}
	if err = a.collect(); err != nil {
		return err
	}
	if err = a.settleHYRevocations(); err != nil {
		return err
	}
	restored := false
	for _, due := range ready {
		u, err := a.store.record(due.id)
		if err != nil {
			return err
		}
		if due.restore {
			oldPeriod := ""
			oldDocument, _ := json.Marshal(u.User)
			if u.Meter != nil {
				oldPeriod = u.Meter.PeriodID
			}
			if !restorePreviousPlan(&u, now) {
				continue
			}
			tx, e := a.store.db.Begin()
			if e != nil {
				return e
			}
			if oldPeriod != "" {
				if _, e = tx.Exec("INSERT INTO quota_periods(user_id,period_id,doc) VALUES(?,?,?) ON CONFLICT(user_id,period_id) DO NOTHING", u.ID, oldPeriod, oldDocument); e != nil {
					tx.Rollback()
					return e
				}
			}
			b, _ := json.Marshal(u.User)
			if _, e = tx.Exec("UPDATE users SET doc=? WHERE id=?", b, u.ID); e != nil {
				tx.Rollback()
				return e
			}
			if e = tx.Commit(); e != nil {
				return e
			}
			restored = true
			continue
		}
		if u.Meter == nil || !u.Meter.PendingReset {
			continue
		}
		m := u.Meter
		oldPeriod := m.PeriodID
		oldDocument, _ := json.Marshal(u.User)
		start := now
		if m.End > 0 && m.End <= now && u.Entitlement != nil {
			start = m.End
			if u.Entitlement.Cycle == "30d" {
				start += (now - start) / (30 * 86400) * (30 * 86400)
			} else if u.Entitlement.Cycle == "month" {
				loc, e := time.LoadLocation(u.Entitlement.Timezone)
				if e != nil {
					return e
				}
				t := time.Unix(now, 0).In(loc)
				start = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc).Unix()
			}
		}
		m.PeriodID = "period-" + randomToken(12)
		m.Start = start
		m.End = 0
		m.PendingReset = false
		m.UploadRemainder, m.DownloadRemainder = 0, 0
		m.BaseUpload, m.BaseDownload = m.Upload, m.Download
		m.RawBaseUpload, m.RawBaseDownload = u.Upload, u.Download
		if u.Entitlement != nil {
			m.End, err = domain.NextPeriod(start, u.Entitlement.Cycle, u.Entitlement.Timezone)
			if err != nil {
				return err
			}
		}
		tx, e := a.store.db.Begin()
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO quota_periods(user_id,period_id,doc) VALUES(?,?,?) ON CONFLICT(user_id,period_id) DO NOTHING", u.ID, oldPeriod, oldDocument); e != nil {
			tx.Rollback()
			return e
		}
		for i := range sites {
			s := &sites[i]
			changed := false
			for j := range s.Grants {
				g := &s.Grants[j]
				if g.UserID != u.ID {
					continue
				}
				if g.Budget == 0 && g.Quota > 0 {
					g.Budget = g.Quota
				}
				if g.Quota > 0 {
					g.Quota, e = domain.AddCounter(s.Usage[u.ID].total(), g.Budget)
					if e != nil {
						tx.Rollback()
						return e
					}
				}
				g.PeriodID = m.PeriodID
				changed = true
			}
			if changed {
				s.Revision = randomToken(12)
				b, e := a.store.vault.seal(s)
				if e != nil {
					tx.Rollback()
					return e
				}
				if _, e = tx.Exec("UPDATE business_sites SET doc=? WHERE id=?", b, s.ID); e != nil {
					tx.Rollback()
					return e
				}
			}
		}
		b, _ := json.Marshal(u.User)
		if _, e = tx.Exec("UPDATE users SET doc=? WHERE id=?", b, u.ID); e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	if restored {
		return a.reconcile()
	}
	return nil
}

// grantsSettled reports whether no business site still holds an issued grant
// for the user; old grants must be withdrawn before a period resets.
func grantsSettled(sites []BusinessSite, userID int64) bool {
	for _, s := range sites {
		if _, issued := s.Issued[userID]; issued || s.IssuedUnlimited[userID] {
			return false
		}
		for _, g := range s.SentGrants {
			if g.UserID == userID {
				return false
			}
		}
	}
	return true
}

// Upstream /kick consumes one marker on a connection's next traffic event.
// Funding a new period or acknowledging a remote withdrawal must first close
// idle old streams and clear their kick markers, including after a restart.
func (a *App) settleHYRevocations() error {
	if a.cfg.Dev || a.oldHYConnections == 0 {
		return nil
	}
	if err := a.collect(); err != nil {
		return err
	}
	if err := restartCore("guangyue-hy2.service"); err != nil {
		return err
	}
	if err := waitPort("127.0.0.1:19199"); err != nil {
		return err
	}
	if err := a.reconcile(); err != nil {
		return err
	}
	if a.oldHYConnections > 0 {
		return fmt.Errorf("old HY2 connections have not been revoked")
	}
	return nil
}
