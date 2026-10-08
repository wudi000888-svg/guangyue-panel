CREATE TABLE crypto_wallet_state (
 id INTEGER PRIMARY KEY CHECK(id=1),
 lock_version INTEGER NOT NULL DEFAULT 0,
 hmac_secret BLOB
);
INSERT INTO crypto_wallet_state(id) VALUES(1);
CREATE TABLE crypto_wallets (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('hot','watch_only')),
 xpub TEXT NOT NULL UNIQUE,
 path TEXT NOT NULL,
 receive_key TEXT NOT NULL UNIQUE,
 first_address TEXT NOT NULL,
 engine TEXT NOT NULL,
 engine_version TEXT NOT NULL,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision>0),
 next_index INTEGER NOT NULL CHECK(next_index>=0 AND next_index<=2147483648),
 reserved_indices INTEGER NOT NULL DEFAULT 0 CHECK(reserved_indices>=0 AND reserved_indices<=2147483648),
 created INTEGER NOT NULL,
 backup_confirmed INTEGER NOT NULL CHECK(backup_confirmed IN (0,1)),
 recovery_required INTEGER NOT NULL DEFAULT 0 CHECK(recovery_required IN (0,1)),
 backup_exported INTEGER NOT NULL DEFAULT 0,
 secret BLOB
);
CREATE TABLE crypto_wallet_addresses (
 id TEXT PRIMARY KEY,
 wallet_id TEXT NOT NULL REFERENCES crypto_wallets(id),
 address_index INTEGER NOT NULL CHECK(address_index>=0 AND address_index<2147483648),
 path TEXT NOT NULL,
 address TEXT NOT NULL UNIQUE,
 label TEXT NOT NULL,
 created INTEGER NOT NULL,
 UNIQUE(wallet_id,address_index)
);
CREATE TABLE crypto_wallet_operations (
 id TEXT PRIMARY KEY,
 actor_id INTEGER NOT NULL,
 fingerprint TEXT NOT NULL,
 response BLOB NOT NULL,
 created INTEGER NOT NULL
);
