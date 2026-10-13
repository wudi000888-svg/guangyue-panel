ALTER TABLE crypto_wallets ADD COLUMN removed_at INTEGER NOT NULL DEFAULT 0 CHECK(removed_at>=0);
CREATE INDEX crypto_wallets_removed_created ON crypto_wallets(removed_at,created);
