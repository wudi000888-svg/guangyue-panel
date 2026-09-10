package controlplane

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/domain"
	"strings"
	"time"
)

type NodeUsage struct {
	UserID       int64  `json:"user_id"`
	NodeID       string `json:"node_id"`
	PeriodID     string `json:"period_id"`
	RateRevision string `json:"rate_revision"`
	RateMilli    int64  `json:"rate_milli"`
	Upload       int64  `json:"upload"`
	Download     int64  `json:"download"`
}

func nodeFromEmail(s string) string {
	_, tail, ok := strings.Cut(s, ".")
	if !ok {
		return "vless-main"
	}
	return strings.TrimSuffix(tail, "@personal")
}
func nodeFromHY(s string) string {
	_, node, ok := strings.Cut(s, "@")
	if !ok {
		return "hy2-main"
	}
	return node
}

func (s *Store) nodeUsage() ([]NodeUsage, error) {
	rows, err := s.db.Query("SELECT user_id,node_id,period_id,rate_revision,rate_milli,upload,download FROM node_usage ORDER BY user_id,node_id,period_id,rate_revision")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeUsage{}
	for rows.Next() {
		var v NodeUsage
		if err = rows.Scan(&v.UserID, &v.NodeID, &v.PeriodID, &v.RateRevision, &v.RateMilli, &v.Upload, &v.Download); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) account(counters []Counter) error {
	if len(counters) == 0 {
		return nil
	}
	nodes, err := s.nodes()
	if err != nil {
		return err
	}
	policies := map[string]Node{}
	for _, n := range nodes {
		policies[n.ID] = normalizeNodePolicy(n)
	}
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// This small retained policy lets a deleted node's final core counters settle
	// without bringing the node or any group membership back into configuration.
	for id, n := range policies {
		b, _ := json.Marshal(Node{ID: id, PolicyVersion: 1, RateMilli: n.RateMilli, RateRevision: n.RateRevision})
		if _, err = tx.Exec("INSERT INTO node_meter_policies(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", id, b); err != nil {
			return err
		}
	}
	users := map[int64]*User{}
	for _, c := range counters {
		if c.UserID <= 0 || c.Value < 0 || c.Value > domain.CounterLimit || c.Direction != "up" && c.Direction != "down" || c.Protocol != "vless" && c.Protocol != "hy2" {
			return errors.New("invalid core counter")
		}
		var gen string
		var last int64
		err = tx.QueryRow("SELECT generation,value FROM checkpoints WHERE key=?", c.Key).Scan(&gen, &last)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		delta := c.Value
		if gen == c.Generation && c.Value >= last {
			delta = c.Value - last
		}
		if _, err = tx.Exec("INSERT INTO checkpoints(key,generation,value) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET generation=excluded.generation,value=excluded.value", c.Key, c.Generation, c.Value); err != nil {
			return err
		}
		if delta == 0 {
			continue
		}
		u := users[c.UserID]
		if u == nil {
			var b []byte
			if err = tx.QueryRow("SELECT doc FROM users WHERE id=?", c.UserID).Scan(&b); errors.Is(err, sql.ErrNoRows) {
				continue
			} else if err != nil {
				return err
			}
			u = &User{}
			if err = json.Unmarshal(b, u); err != nil {
				return err
			}
			u.InitMeter("legacy-"+businessUsageKey(c.UserID), time.Now().Unix())
			users[c.UserID] = u
		}
		nodeID := c.NodeID
		if nodeID == "" {
			nodeID = c.Protocol + "-main"
		}
		n, ok := policies[nodeID]
		if !ok {
			var b []byte
			err = tx.QueryRow("SELECT doc FROM node_meter_policies WHERE id=?", nodeID).Scan(&b)
			if err == nil {
				if err = json.Unmarshal(b, &n); err != nil {
					return err
				}
			} else if errors.Is(err, sql.ErrNoRows) {
				n = normalizeNodePolicy(Node{ID: nodeID})
			} else {
				return err
			}
		}
		var raw, weighted, residue, protocol *int64
		if c.Direction == "up" {
			raw, weighted, residue = &u.Upload, &u.Meter.Upload, &u.Meter.UploadRemainder
		} else {
			raw, weighted, residue = &u.Download, &u.Meter.Download, &u.Meter.DownloadRemainder
		}
		if c.Protocol == "vless" {
			protocol = &u.VLESSTraffic
		} else {
			protocol = &u.HY2Traffic
		}
		cost, rem, e := domain.WeightedBytes(delta, n.RateMilli, *residue)
		if e != nil {
			return e
		}
		if *raw, err = domain.AddCounter(*raw, delta); err != nil {
			return err
		}
		if *protocol, err = domain.AddCounter(*protocol, delta); err != nil {
			return err
		}
		if *weighted, err = domain.AddCounter(*weighted, cost); err != nil {
			return err
		}
		*residue = rem
		up, down := int64(0), int64(0)
		if c.Direction == "up" {
			up = delta
		} else {
			down = delta
		}
		if _, err = tx.Exec("INSERT INTO node_usage(user_id,node_id,period_id,rate_revision,rate_milli,upload,download) VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,node_id,period_id,rate_revision) DO UPDATE SET upload=node_usage.upload+excluded.upload,download=node_usage.download+excluded.download", c.UserID, nodeID, u.Meter.PeriodID, n.RateRevision, n.RateMilli, up, down); err != nil {
			return err
		}
		if s.logsEnabledLocked() {
			if _, err = tx.Exec("INSERT INTO traffic(hour,user_id,upload,download) VALUES(?,?,?,?) ON CONFLICT(hour,user_id) DO UPDATE SET upload=traffic.upload+excluded.upload,download=traffic.download+excluded.download", time.Now().Unix()/3600*3600, c.UserID, up, down); err != nil {
				return err
			}
		}
	}
	for id, u := range users {
		b, e := json.Marshal(u)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("UPDATE users SET doc=? WHERE id=?", b, id); err != nil {
			return err
		}
	}
	if s.logsEnabledLocked() {
		if _, err = tx.Exec("DELETE FROM traffic WHERE hour<?", time.Now().Add(-30*24*time.Hour).Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
