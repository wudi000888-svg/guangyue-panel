-- Public Solana descriptors contain no seed/private material. The common
-- receiving high-water namespace remains crypto_wallets.next_index.
CREATE TABLE crypto_solana_wallet_public (
 wallet_id TEXT PRIMARY KEY REFERENCES crypto_wallet_history(id),
 first_address TEXT NOT NULL,
 funding_address TEXT NOT NULL,
 funding_path TEXT NOT NULL,
 doc BLOB NOT NULL,
 created INTEGER NOT NULL
);
-- A Solana recent blockhash is part of the immutable signed envelope. An
-- expired/unknown transaction is never automatically replaced and re-signed.
CREATE TABLE crypto_solana_transactions (
 id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL REFERENCES crypto_sweep_jobs(id),
 item_id TEXT NOT NULL UNIQUE REFERENCES crypto_sweep_items(id),
 chain_id INTEGER NOT NULL CHECK(chain_id=101),
 signature TEXT NOT NULL UNIQUE,
 raw BLOB NOT NULL,
 message BLOB NOT NULL,
 intent BLOB NOT NULL,
 last_valid_height INTEGER NOT NULL,
 state TEXT NOT NULL,
 receipt BLOB NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '',
 created INTEGER NOT NULL,
 updated INTEGER NOT NULL
);
CREATE INDEX crypto_solana_transactions_jobs ON crypto_solana_transactions(job_id,state);

CREATE TABLE crypto_solana_address_proofs (
 address_id TEXT PRIMARY KEY REFERENCES crypto_wallet_addresses(id),
 doc BLOB NOT NULL
);
