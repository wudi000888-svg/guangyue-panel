DROP INDEX commerce_order_active;
CREATE UNIQUE INDEX commerce_order_active ON commerce_orders(user_id) WHERE state IN ('pending','provisioning','refund_requested','refunding');
