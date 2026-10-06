package controlplane

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
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

type closedBusinessDelta struct {
	upload, download           int64
	quotaUpload, quotaDownload int64
}

type closedBusinessPeriod struct {
	user                               User
	uploadRemainder, downloadRemainder int64
}

type closedBusinessPeriodKey struct {
	userID   int64
	periodID string
}

// businessNodeProtocol is used only for repairing an archived period. A node
// usage row intentionally carries no protocol field, so resolve it from the
// last policy that could have issued the row. Unknown legacy node IDs retain
// the historical vless default.
func businessNodeProtocol(v *BusinessSite, id string) string {
	if v.Mount != nil {
		for _, n := range v.Mount.Catalog.Nodes {
			if n.ID == id {
				return n.Protocol
			}
		}
	}
	for _, nodes := range [][]Node{v.SentNodes, v.IssuedNodes, v.Nodes, v.DefaultNodes} {
		for _, n := range nodes {
			if n.ID == id {
				return n.Protocol
			}
		}
	}
	if id == "hy2-main" || strings.HasPrefix(id, "hy2-") {
		return "hy2"
	}
	return "vless"
}

// reconcileClosedBusinessUsage accepts traffic that was sampled by a child
// after the controller had already closed that user's period. The old code
// rejected the complete heartbeat, which left the mount permanently waiting
// for confirmation. Keep the old period's historical document accurate and
// return the weighted amount so the current period baseline can be rebased.
// This function runs in the same transaction as the live user update.
func (a *App) reconcileClosedBusinessUsage(tx *persistence.Tx, v *BusinessSite, rows []NodeUsage) (map[int64]closedBusinessDelta, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	ordered := append([]NodeUsage{}, rows...)
	sort.Slice(ordered, func(i, j int) bool { return usageIdentity(ordered[i]) < usageIdentity(ordered[j]) })
	periods := map[closedBusinessPeriodKey]*closedBusinessPeriod{}
	deltas := map[int64]closedBusinessDelta{}
	for _, row := range ordered {
		var up, down int64
		err := tx.QueryRow("SELECT upload,download FROM business_node_usage WHERE site_id=? AND user_id=? AND node_id=? AND period_id=? AND rate_revision=?", v.ID, row.UserID, row.NodeID, row.PeriodID, row.RateRevision).Scan(&up, &down)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		du, dd := row.Upload-up, row.Download-down
		if du <= 0 && dd <= 0 {
			continue
		}
		var doc []byte
		err = tx.QueryRow("SELECT doc FROM quota_periods WHERE user_id=? AND period_id=?", row.UserID, row.PeriodID).Scan(&doc)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		key := closedBusinessPeriodKey{userID: row.UserID, periodID: row.PeriodID}
		state := periods[key]
		if state == nil {
			state = &closedBusinessPeriod{}
			if err = json.Unmarshal(doc, &state.user); err != nil {
				return nil, err
			}
			state.user.InitMeter(row.PeriodID, state.user.Created)
			if state.user.Meter.PeriodID == "" {
				state.user.Meter.PeriodID = row.PeriodID
			}
			state.uploadRemainder = state.user.Meter.UploadRemainder
			state.downloadRemainder = state.user.Meter.DownloadRemainder
			periods[key] = state
		}
		qu, nextUploadRemainder, err := domain.WeightedBytes(du, row.RateMilli, state.uploadRemainder)
		if err != nil {
			return nil, err
		}
		qd, nextDownloadRemainder, err := domain.WeightedBytes(dd, row.RateMilli, state.downloadRemainder)
		if err != nil {
			return nil, err
		}
		state.user.Upload, err = domain.AddCounter(state.user.Upload, du)
		if err != nil {
			return nil, err
		}
		state.user.Download, err = domain.AddCounter(state.user.Download, dd)
		if err != nil {
			return nil, err
		}
		if businessNodeProtocol(v, row.NodeID) == "hy2" {
			state.user.HY2Traffic, err = domain.AddCounter(state.user.HY2Traffic, du)
			if err == nil {
				state.user.HY2Traffic, err = domain.AddCounter(state.user.HY2Traffic, dd)
			}
		} else {
			state.user.VLESSTraffic, err = domain.AddCounter(state.user.VLESSTraffic, du)
			if err == nil {
				state.user.VLESSTraffic, err = domain.AddCounter(state.user.VLESSTraffic, dd)
			}
		}
		if err != nil {
			return nil, err
		}
		state.user.Meter.Upload, err = domain.AddCounter(state.user.Meter.Upload, qu)
		if err != nil {
			return nil, err
		}
		state.user.Meter.Download, err = domain.AddCounter(state.user.Meter.Download, qd)
		if err != nil {
			return nil, err
		}
		state.uploadRemainder, state.downloadRemainder = nextUploadRemainder, nextDownloadRemainder
		delta := deltas[row.UserID]
		delta.upload, err = domain.AddCounter(delta.upload, du)
		if err != nil {
			return nil, err
		}
		delta.download, err = domain.AddCounter(delta.download, dd)
		if err != nil {
			return nil, err
		}
		delta.quotaUpload, err = domain.AddCounter(delta.quotaUpload, qu)
		if err != nil {
			return nil, err
		}
		delta.quotaDownload, err = domain.AddCounter(delta.quotaDownload, qd)
		if err != nil {
			return nil, err
		}
		deltas[row.UserID] = delta
	}
	for key, state := range periods {
		state.user.Meter.UploadRemainder = state.uploadRemainder
		state.user.Meter.DownloadRemainder = state.downloadRemainder
		doc, err := json.Marshal(state.user)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE quota_periods SET doc=? WHERE user_id=? AND period_id=?", doc, key.userID, key.periodID); err != nil {
			return nil, err
		}
	}
	return deltas, nil
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
