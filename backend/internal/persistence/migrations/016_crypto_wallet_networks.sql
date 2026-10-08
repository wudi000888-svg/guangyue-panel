ALTER TABLE crypto_wallets ADD COLUMN supported_chain_ids TEXT NOT NULL DEFAULT '[1,56]';
