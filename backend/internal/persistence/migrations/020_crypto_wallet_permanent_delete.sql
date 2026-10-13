CREATE TABLE crypto_wallet_history (
 id TEXT PRIMARY KEY,
 receive_hash TEXT NOT NULL CHECK(length(receive_hash)=64),
 next_index INTEGER NOT NULL CHECK(next_index>=0 AND next_index<=2147483648),
 deleted_at INTEGER NOT NULL DEFAULT 0 CHECK(deleted_at>=0)
);
CREATE INDEX crypto_wallet_history_descriptor ON crypto_wallet_history(receive_hash,next_index);
