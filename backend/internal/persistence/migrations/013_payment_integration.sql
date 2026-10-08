DROP INDEX payment_provider_code;
CREATE INDEX payment_provider_adapter ON payment_providers(code);
ALTER TABLE payment_providers ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
ALTER TABLE payment_attempts ADD COLUMN purpose TEXT NOT NULL DEFAULT 'order';
ALTER TABLE payment_attempts ADD COLUMN message TEXT NOT NULL DEFAULT '';
ALTER TABLE payment_attempts ADD COLUMN account_scope TEXT NOT NULL DEFAULT '';
CREATE INDEX payment_attempt_recovery ON payment_attempts(state,updated);
CREATE INDEX payment_attempt_merchant ON payment_attempts(provider_id,merchant_ref);
CREATE TABLE payment_receipts (
 id TEXT PRIMARY KEY,
 account_scope TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 external_ref TEXT NOT NULL,
 attempt_id TEXT NOT NULL DEFAULT '',
 order_id TEXT NOT NULL DEFAULT '',
 user_id INTEGER NOT NULL DEFAULT 0,
 amount INTEGER NOT NULL CHECK(amount>0),
 currency TEXT NOT NULL,
 state TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 created INTEGER NOT NULL,
 updated INTEGER NOT NULL,
 UNIQUE(account_scope,external_ref)
);
CREATE INDEX payment_receipt_work ON payment_receipts(state,updated);
CREATE INDEX payment_receipt_attempt ON payment_receipts(attempt_id);
CREATE TABLE payment_refunds (
 id TEXT PRIMARY KEY,
 receipt_id TEXT NOT NULL UNIQUE,
 account_scope TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 state TEXT NOT NULL,
 amount INTEGER NOT NULL CHECK(amount>0),
 currency TEXT NOT NULL,
 external_ref TEXT NOT NULL DEFAULT '',
 actor_id INTEGER NOT NULL,
 reason TEXT NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL DEFAULT '',
 created INTEGER NOT NULL,
 updated INTEGER NOT NULL
);
CREATE INDEX payment_refund_work ON payment_refunds(state,next_attempt,updated);
CREATE UNIQUE INDEX payment_refund_external ON payment_refunds(account_scope,external_ref) WHERE external_ref<>'';
