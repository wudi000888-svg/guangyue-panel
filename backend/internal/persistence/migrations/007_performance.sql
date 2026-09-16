-- Dashboard member views filter by user before scanning the retained traffic
-- window. The primary key is ordered by hour for owner-wide reports, so keep a
-- second covering index for the per-member path and MIN(hour) lookup.
CREATE INDEX IF NOT EXISTS traffic_user_hour_totals
  ON traffic(user_id, hour, upload, download);
