package controlplane

import (
	"encoding/json"
	"fmt"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"time"
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
		if (assigned || issued || s.IssuedUnlimited[u.ID]) && (s.Info == nil || s.Info.Protocol < 2) && (u.Entitlement != nil || action == "reset") {
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
	for _, u := range users {
		if pending, e := a.store.commerceBarrier(u.ID); e != nil {
			return e
		} else if pending {
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
		if !m.PendingReset {
			continue
		}
		waiting := false
		for _, s := range sites {
			_, issued := s.Issued[u.ID]
			waiting = waiting || issued || s.IssuedUnlimited[u.ID]
			for _, g := range s.SentGrants {
				if g.UserID == u.ID {
					waiting = true
				}
			}
		}
		if waiting {
			continue
		}
		if err = a.reconcile(); err != nil {
			return err
		}
		if err = a.collect(); err != nil {
			return err
		}
		if err = a.settleHYRevocations(); err != nil {
			return err
		}

		u, err = a.store.record(u.ID)
		if err != nil {
			return err
		}
		m = u.Meter
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
	return nil
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
