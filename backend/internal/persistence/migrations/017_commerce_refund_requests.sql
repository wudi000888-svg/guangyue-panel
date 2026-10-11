CREATE TABLE commerce_refund_requests (
    id TEXT PRIMARY KEY,
    order_id TEXT NOT NULL REFERENCES commerce_orders(id),
    user_id INTEGER NOT NULL,
    state TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    reason TEXT NOT NULL DEFAULT '',
    actor_id INTEGER NOT NULL DEFAULT 0,
    created INTEGER NOT NULL,
    updated INTEGER NOT NULL,
    decided_by INTEGER NOT NULL DEFAULT 0,
    decided_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX commerce_refund_request_order ON commerce_refund_requests(order_id,created,id);
CREATE INDEX commerce_refund_request_user ON commerce_refund_requests(user_id,created,id);
CREATE UNIQUE INDEX commerce_refund_request_pending ON commerce_refund_requests(order_id) WHERE state='requested';
DROP INDEX commerce_order_active;
CREATE UNIQUE INDEX commerce_order_active ON commerce_orders(user_id) WHERE state IN ('pending','provisioning','refunding');
INSERT INTO commerce_refund_requests(id,order_id,user_id,state,revision,reason,actor_id,created,updated)
SELECT 'legacy-refund-' || id,id,user_id,'requested',1,'',0,created,updated
FROM commerce_orders WHERE state='refund_requested';
UPDATE commerce_orders SET state='completed',updated=updated WHERE state='refund_requested';
