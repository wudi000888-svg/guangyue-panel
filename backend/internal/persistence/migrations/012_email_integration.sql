CREATE TABLE email_settings (id INTEGER PRIMARY KEY, version INTEGER NOT NULL, doc BLOB NOT NULL);
CREATE TABLE email_accounts (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, email TEXT NOT NULL UNIQUE, verified_at INTEGER NOT NULL);
CREATE TABLE email_tokens (token_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL DEFAULT 0, email TEXT NOT NULL, purpose TEXT NOT NULL, password_hash TEXT NOT NULL DEFAULT '', expires INTEGER NOT NULL, consumed INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL);
CREATE INDEX email_token_account ON email_tokens(user_id,purpose,consumed,created);
CREATE INDEX email_token_expiry ON email_tokens(expires);
CREATE TABLE email_outbox (id TEXT PRIMARY KEY, user_id INTEGER NOT NULL DEFAULT 0, recipient TEXT NOT NULL, kind TEXT NOT NULL, body BLOB NOT NULL, state TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0, next_attempt INTEGER NOT NULL, lease_token TEXT NOT NULL DEFAULT '', lease_until INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', expires INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL, sent_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX email_outbox_ready ON email_outbox(state,next_attempt,lease_until);
CREATE TABLE email_event_receipts (event_id TEXT PRIMARY KEY, state TEXT NOT NULL, created INTEGER NOT NULL);
CREATE TABLE email_rate_limits (key TEXT NOT NULL, bucket INTEGER NOT NULL, count INTEGER NOT NULL, PRIMARY KEY(key,bucket));
