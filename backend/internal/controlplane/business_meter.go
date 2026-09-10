package controlplane

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
)

func businessUsageOf(u User) BusinessUsage {
	v := BusinessUsage{Metered: true, QuotaUpload: u.Upload, QuotaDownload: u.Download, Upload: u.Upload, Download: u.Download, VLESS: u.VLESSTraffic, HY2: u.HY2Traffic}
	if u.Meter != nil {
		v.Metered = true
		v.PeriodID = u.Meter.PeriodID
		v.QuotaUpload = u.Meter.Upload
		v.QuotaDownload = u.Meter.Download
		v.UploadRemainder = u.Meter.UploadRemainder
		v.DownloadRemainder = u.Meter.DownloadRemainder
	}
	return v
}
func usageIdentity(v NodeUsage) string {
	return fmt.Sprintf("%d/%s/%s/%s", v.UserID, v.NodeID, v.PeriodID, v.RateRevision)
}

// Verify the agent's weighted total against immutable issued rate revisions.
// Old aggregates have a one-time 1x baseline; no invented per-node history.
func (a *App) verifyBusinessMeter(v *BusinessSite, in BusinessHeartbeat) error {
	if in.Protocol < 2 {
		if v.Info != nil && v.Info.Protocol >= 2 {
			return errors.New("meter protocol downgrade rejected")
		}
		return nil
	}
	if len(in.NodeUsage) > 32768 {
		return errors.New("node usage report too large")
	}
	type sum struct{ up, down, qup, qdown, ru, rd int64 }
	sums := map[int64]*sum{}
	for id, next := range in.Usage {
		if !next.Metered {
			return errors.New("missing weighted traffic")
		}
		old := v.Usage[id]
		s := &sum{up: old.Upload, down: old.Download, qup: old.Upload, qdown: old.Download}
		if old.Metered {
			s.qup, s.qdown, s.ru, s.rd = old.QuotaUpload, old.QuotaDownload, old.UploadRemainder, old.DownloadRemainder
		}
		base, ok := in.UsageBaseline[id]
		if !ok || base.Upload < 0 || base.Download < 0 || base.Upload > domain.CounterLimit || base.Download > domain.CounterLimit {
			return errors.New("invalid legacy usage baseline")
		}
		if prior, ok := v.UsageBaseline[id]; ok {
			if prior.Upload != base.Upload || prior.Download != base.Download {
				return errors.New("legacy usage baseline changed")
			}
		} else {
			if old.Metered && (base.Upload != 0 || base.Download != 0) {
				return errors.New("unexpected usage baseline")
			}
			if !old.Metered {
				if base.Upload < old.Upload || base.Download < old.Download {
					return errors.New("incomplete legacy baseline")
				}
				s.up, s.down, s.qup, s.qdown = base.Upload, base.Download, base.Upload, base.Download
			}
			v.UsageBaseline[id] = base
		}
		if next.PeriodID != old.PeriodID && old.Metered {
			if !v.PeriodRules[businessUsageKey(id)+"/"+next.PeriodID] {
				return errors.New("unissued quota period")
			}
			s.ru, s.rd = 0, 0
		}
		sums[id] = s
	}
	seen := map[string]bool{}
	for _, row := range in.NodeUsage {
		key := usageIdentity(row)
		if seen[key] {
			return errors.New("duplicate node usage row")
		}
		seen[key] = true
		if row.UserID <= 0 || row.NodeID == "" || row.Upload < 0 || row.Download < 0 {
			return errors.New("invalid node usage row")
		}
		rate, issued := v.RateRules[row.NodeID+"/"+row.RateRevision]
		if !issued || rate != row.RateMilli || !v.PeriodRules[businessUsageKey(row.UserID)+"/"+row.PeriodID] {
			return errors.New("usage rule or quota period was not issued")
		}
		var up, down int64
		err := a.store.db.QueryRow("SELECT upload,download FROM business_node_usage WHERE site_id=? AND user_id=? AND node_id=? AND period_id=? AND rate_revision=?", v.ID, row.UserID, row.NodeID, row.PeriodID, row.RateRevision).Scan(&up, &down)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if row.Upload < up || row.Download < down {
			return errors.New("node usage watermark moved backwards")
		}
		du, dd := row.Upload-up, row.Download-down
		if du > 0 || dd > 0 {
			var closed int
			if err = a.store.db.QueryRow("SELECT COUNT(*) FROM quota_periods WHERE user_id=? AND period_id=?", row.UserID, row.PeriodID).Scan(&closed); err != nil {
				return err
			}
			if closed > 0 {
				return errors.New("settled quota period received new traffic; site reconciliation required")
			}
		}
		s := sums[row.UserID]
		if s == nil {
			return errors.New("missing aggregate usage")
		}
		if s.up, err = domain.AddCounter(s.up, du); err != nil {
			return err
		}
		if s.down, err = domain.AddCounter(s.down, dd); err != nil {
			return err
		}
		qu, ru, err := domain.WeightedBytes(du, rate, s.ru)
		if err != nil {
			return err
		}
		qd, rd, err := domain.WeightedBytes(dd, rate, s.rd)
		if err != nil {
			return err
		}
		if s.qup, err = domain.AddCounter(s.qup, qu); err != nil {
			return err
		}
		if s.qdown, err = domain.AddCounter(s.qdown, qd); err != nil {
			return err
		}
		s.ru, s.rd = ru, rd
	}
	for id, next := range in.Usage {
		s := sums[id]
		if next.Upload != s.up || next.Download != s.down || next.QuotaUpload != s.qup || next.QuotaDownload != s.qdown || next.UploadRemainder != s.ru || next.DownloadRemainder != s.rd {
			return errors.New("weighted usage verification failed")
		}
	}
	return nil
}
