package controlplane

import "errors"

func (s *Store) pendingNodeUsage() ([]NodeUsage, error) {
	rows, err := s.db.Query("SELECT n.user_id,n.node_id,n.period_id,n.rate_revision,n.rate_milli,n.upload,n.download FROM node_usage n LEFT JOIN business_usage_acks a ON n.user_id=a.user_id AND n.node_id=a.node_id AND n.period_id=a.period_id AND n.rate_revision=a.rate_revision WHERE a.user_id IS NULL OR n.upload>a.upload OR n.download>a.download ORDER BY n.user_id,n.node_id,n.period_id,n.rate_revision")
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
func (s *Store) ackBusinessUsage(rows []NodeUsage) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range rows {
		var up, down, rate int64
		if err = tx.QueryRow("SELECT upload,download,rate_milli FROM node_usage WHERE user_id=? AND node_id=? AND period_id=? AND rate_revision=?", v.UserID, v.NodeID, v.PeriodID, v.RateRevision).Scan(&up, &down, &rate); err != nil {
			return err
		}
		if v.Upload < 0 || v.Download < 0 || v.Upload > up || v.Download > down || v.RateMilli != rate {
			return errors.New("invalid business usage acknowledgement")
		}
		if _, err = tx.Exec("INSERT INTO business_usage_acks(user_id,node_id,period_id,rate_revision,upload,download) VALUES(?,?,?,?,?,?) ON CONFLICT(user_id,node_id,period_id,rate_revision) DO UPDATE SET upload=excluded.upload,download=excluded.download WHERE business_usage_acks.upload<=excluded.upload AND business_usage_acks.download<=excluded.download", v.UserID, v.NodeID, v.PeriodID, v.RateRevision, v.Upload, v.Download); err != nil {
			return err
		}
	}
	return tx.Commit()
}
