CREATE TABLE crypto_payment_settings(id INTEGER PRIMARY KEY CHECK(id=1),revision INTEGER NOT NULL,doc BLOB NOT NULL);
CREATE TABLE crypto_invoices (
 id TEXT PRIMARY KEY, order_id TEXT NOT NULL REFERENCES commerce_orders(id), user_id INTEGER NOT NULL,
 attempt_id TEXT NOT NULL UNIQUE REFERENCES payment_attempts(id), operation_id TEXT NOT NULL UNIQUE, fingerprint TEXT NOT NULL,
 wallet_id TEXT NOT NULL REFERENCES crypto_wallets(id), address_id TEXT NOT NULL UNIQUE REFERENCES crypto_wallet_addresses(id),
 chain_id INTEGER NOT NULL, asset_id TEXT NOT NULL, address TEXT NOT NULL UNIQUE, contract TEXT NOT NULL,
 expected_atoms TEXT NOT NULL, state TEXT NOT NULL, created INTEGER NOT NULL, expires INTEGER NOT NULL,
 start_block INTEGER NOT NULL, next_block INTEGER NOT NULL, last_scan INTEGER NOT NULL DEFAULT 0,
 finalized_block INTEGER NOT NULL DEFAULT 0, scan_error TEXT NOT NULL DEFAULT '', doc BLOB NOT NULL
);
CREATE INDEX crypto_invoice_order ON crypto_invoices(order_id,created);
CREATE INDEX crypto_invoice_scan ON crypto_invoices(last_scan,id);
CREATE TABLE crypto_invoice_chains(invoice_id TEXT NOT NULL REFERENCES crypto_invoices(id),chain_id INTEGER NOT NULL,next_block INTEGER NOT NULL,finalized_hash TEXT NOT NULL DEFAULT '',PRIMARY KEY(invoice_id,chain_id));
CREATE TABLE crypto_transfers (
 chain_id INTEGER NOT NULL, tx_hash TEXT NOT NULL, log_index INTEGER NOT NULL,
 invoice_id TEXT NOT NULL REFERENCES crypto_invoices(id), contract TEXT NOT NULL, sender TEXT NOT NULL, recipient TEXT NOT NULL,
 atoms TEXT NOT NULL, block_number INTEGER NOT NULL, block_hash TEXT NOT NULL, block_time INTEGER NOT NULL,
 state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL, updated INTEGER NOT NULL,
 PRIMARY KEY(chain_id,tx_hash,log_index)
);
CREATE INDEX crypto_transfer_invoice ON crypto_transfers(invoice_id,state);
CREATE TABLE crypto_transfer_observations(chain_id INTEGER NOT NULL,tx_hash TEXT NOT NULL,log_index INTEGER NOT NULL,block_hash TEXT NOT NULL,invoice_id TEXT NOT NULL REFERENCES crypto_invoices(id),contract TEXT NOT NULL,atoms TEXT NOT NULL,block_number INTEGER NOT NULL,block_time INTEGER NOT NULL,PRIMARY KEY(chain_id,tx_hash,block_hash,log_index));
CREATE INDEX crypto_observation_invoice ON crypto_transfer_observations(invoice_id,chain_id,block_number);
CREATE TABLE crypto_chain_state(chain_id INTEGER PRIMARY KEY,frozen INTEGER NOT NULL DEFAULT 0,reason TEXT NOT NULL DEFAULT '',updated INTEGER NOT NULL);
CREATE TABLE crypto_funding_addresses(wallet_id TEXT PRIMARY KEY REFERENCES crypto_wallets(id),address TEXT NOT NULL UNIQUE,path TEXT NOT NULL,created INTEGER NOT NULL);
CREATE TABLE crypto_sweep_jobs(id TEXT PRIMARY KEY,actor_id INTEGER NOT NULL,operation_id TEXT NOT NULL UNIQUE,fingerprint TEXT NOT NULL,wallet_id TEXT NOT NULL REFERENCES crypto_wallets(id),chain_id INTEGER NOT NULL,asset_id TEXT NOT NULL,destination TEXT NOT NULL,min_atoms TEXT NOT NULL,max_gas_atoms TEXT NOT NULL,state TEXT NOT NULL,doc BLOB NOT NULL,config BLOB NOT NULL,error TEXT NOT NULL DEFAULT '',lease_token TEXT NOT NULL DEFAULT '',lease_until INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL,updated INTEGER NOT NULL);
CREATE UNIQUE INDEX crypto_one_active_sweep ON crypto_sweep_jobs(wallet_id,chain_id) WHERE state NOT IN ('complete','cancelled');
CREATE UNIQUE INDEX crypto_sweep_quote_once ON crypto_sweep_jobs(actor_id,fingerprint);
CREATE TABLE crypto_sweep_items(id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES crypto_sweep_jobs(id),address_id TEXT NOT NULL,address TEXT NOT NULL,path TEXT NOT NULL,amount_atoms TEXT NOT NULL,gas_needed_atoms TEXT NOT NULL DEFAULT '0',state TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',UNIQUE(job_id,address_id));
CREATE TABLE crypto_chain_transactions(id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES crypto_sweep_jobs(id),item_id TEXT NOT NULL REFERENCES crypto_sweep_items(id),kind TEXT NOT NULL,chain_id INTEGER NOT NULL,from_address TEXT NOT NULL,to_address TEXT NOT NULL,token TEXT NOT NULL,amount_atoms TEXT NOT NULL,nonce INTEGER NOT NULL,tx_hash TEXT NOT NULL UNIQUE,raw BLOB NOT NULL,gas_limit INTEGER NOT NULL,gas_price_atoms TEXT NOT NULL,state TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',block_number INTEGER NOT NULL DEFAULT 0,block_hash TEXT NOT NULL DEFAULT '',receipt BLOB NOT NULL DEFAULT '',created INTEGER NOT NULL,updated INTEGER NOT NULL,UNIQUE(chain_id,from_address,nonce),UNIQUE(job_id,item_id,kind));
