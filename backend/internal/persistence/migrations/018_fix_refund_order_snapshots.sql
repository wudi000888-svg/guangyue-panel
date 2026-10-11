-- Migration 017 moved legacy refund requests back to completed orders but did
-- not update the embedded order document. The control-plane startup repair
-- rewrites that JSON using the database driver's native byte representation.
-- Keep a durable marker so old installations run the repair after upgrading.
INSERT INTO meta(key,value) VALUES('commerce_order_snapshot_repair','v1')
ON CONFLICT(key) DO UPDATE SET value='v1';
