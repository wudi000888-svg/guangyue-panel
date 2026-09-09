package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type TrafficBucket struct {
	At       int64 `json:"at"`
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
}
type TrafficLeader struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}
type DashboardTraffic struct {
	Range               string          `json:"range"`
	Start               int64           `json:"start"`
	End                 int64           `json:"end"`
	BucketSeconds       int64           `json:"bucket_seconds"`
	Upload              int64           `json:"upload"`
	Download            int64           `json:"download"`
	Today               int64           `json:"today"`
	Previous            int64           `json:"previous"`
	ComparisonAvailable bool            `json:"comparison_available"`
	FirstRecord         int64           `json:"first_record"`
	Buckets             []TrafficBucket `json:"buckets"`
	Leaders             []TrafficLeader `json:"leaders"`
}

func (s *Store) dashboardTraffic(actor Record, period string, offset int64, now int64) (DashboardTraffic, error) {
	step, count := int64(3600), int64(24)
	day := (now+offset)/86400*86400 - offset
	end := (now/3600 + 1) * 3600
	if period != "24h" {
		step = 86400
		count = 7
		if period == "30d" {
			count = 30
		}
		end = day + 86400
	}
	start := end - step*count
	previousStart := start - step*count
	d := DashboardTraffic{Range: period, Start: start, End: end, BucketSeconds: step, Buckets: []TrafficBucket{}, Leaders: []TrafficLeader{}}
	filter := ""
	args := []any{max(previousStart, now-30*86400), end}
	if actor.Role != "owner" {
		filter = " AND user_id=?"
		args = append(args, actor.ID)
	}
	rows, err := s.db.Query("SELECT hour,SUM(upload),SUM(download) FROM traffic WHERE hour>=? AND hour<?"+filter+" GROUP BY hour ORDER BY hour", args...)
	if err != nil {
		return d, err
	}
	byTime := map[int64]TrafficBucket{}
	for rows.Next() {
		var at, up, down int64
		if err = rows.Scan(&at, &up, &down); err != nil {
			rows.Close()
			return d, err
		}
		if at >= day {
			d.Today += up + down
		}
		if at < start {
			d.Previous += up + down
			continue
		}
		bucket := start + (at-start)/step*step
		b := byTime[bucket]
		b.At = bucket
		b.Upload += up
		b.Download += down
		byTime[bucket] = b
		d.Upload += up
		d.Download += down
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	for at := start; at < end; at += step {
		v := byTime[at]
		v.At = at
		d.Buckets = append(d.Buckets, v)
	}
	var first sql.NullInt64
	query := "SELECT MIN(hour) FROM traffic"
	minArgs := []any{}
	if actor.Role != "owner" {
		query += " WHERE user_id=?"
		minArgs = append(minArgs, actor.ID)
	}
	if err = s.db.QueryRow(query, minArgs...).Scan(&first); err != nil {
		return d, err
	}
	d.FirstRecord = first.Int64
	d.ComparisonAvailable = first.Valid && first.Int64 <= previousStart && previousStart >= now-30*86400
	args = []any{start, end}
	if actor.Role != "owner" {
		args = append(args, actor.ID)
	}
	rows, err = s.db.Query("SELECT t.user_id,u.username,SUM(t.upload),SUM(t.download) FROM traffic t JOIN users u ON u.id=t.user_id WHERE t.hour>=? AND t.hour<?"+filter+" GROUP BY t.user_id,u.username ORDER BY SUM(t.upload+t.download) DESC,t.user_id LIMIT 5", args...)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var v TrafficLeader
		if err = rows.Scan(&v.UserID, &v.Username, &v.Upload, &v.Download); err != nil {
			return d, err
		}
		d.Leaders = append(d.Leaders, v)
	}
	return d, rows.Err()
}
func (a *App) dashboard(w http.ResponseWriter, r *http.Request, actor Record) {
	period := r.URL.Query().Get("range")
	if period == "" {
		period = "24h"
	}
	if period != "24h" && period != "7d" && period != "30d" {
		failure(w, 400, "请选择 24 小时、7 天或 30 天")
		return
	}
	offset := int64(0)
	if raw := r.URL.Query().Get("tz"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < -720 || v > 840 {
			failure(w, 400, "时区参数无效")
			return
		}
		offset = v * 60
	}
	now := time.Now().Unix()
	data, err := a.store.dashboardTraffic(actor, period, offset, now)
	if err != nil {
		failure(w, 500, "读取仪表盘统计失败")
		return
	}
	result := object{"traffic": data, "generated_at": now, "retention_days": 30}
	if actor.Role == "owner" {
		sources, err := a.store.importSources()
		if err != nil {
			failure(w, 500, "读取订阅任务失败")
			return
		}
		enabled, queued, failed, stale := 0, 0, 0, 0
		next := int64(0)
		for _, s := range sources {
			if s.Enabled {
				enabled++
				if next == 0 || s.NextAt < next {
					next = s.NextAt
				}
			}
			if s.Queued {
				queued++
			}
			if s.Error != "" {
				failed++
			}
		}
		pools, err := a.store.pools()
		if err != nil {
			failure(w, 500, "读取 IP 池失败")
			return
		}
		for _, p := range pools {
			if p.SourceStale {
				stale++
			}
		}
		result["subscriptions"] = object{"total": len(sources), "enabled": enabled, "queued": queued, "failed": failed, "stale_resources": stale, "next_at": next}
		a.publicMu.Lock()
		status := a.publicStatus
		if status.StartedAt == 0 {
			_ = json.Unmarshal([]byte(a.store.meta("public_status")), &status)
			status.Running = false
		}
		a.publicMu.Unlock()
		c := a.store.publicSettings()
		blocks, err := a.store.publicBlocks(c)
		if err != nil {
			failure(w, 500, "读取公共池统计失败")
			return
		}
		result["public"] = object{"enabled": c.Enabled, "max_resources": c.MaxResources, "running": status.Running, "phase": status.Phase, "checked": status.Checked, "discovered": status.Discovered, "skipped": status.Skipped, "added": status.Added, "removed": status.Removed, "finished_at": status.FinishedAt, "next_at": status.FinishedAt + int64(c.IntervalMinutes*60), "blacklist": len(blocks), "error": status.Error, "sources": a.publicSourceStates()}
	}
	jsonResponse(w, 200, result)
}
