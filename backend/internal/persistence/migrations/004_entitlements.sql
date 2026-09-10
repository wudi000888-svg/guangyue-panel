CREATE TABLE IF NOT EXISTS node_groups (id TEXT PRIMARY KEY, doc BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plans (id TEXT PRIMARY KEY, doc BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plan_versions (plan_id TEXT NOT NULL, version INTEGER NOT NULL, doc BLOB NOT NULL, PRIMARY KEY(plan_id,version));
CREATE TABLE IF NOT EXISTS quota_periods (user_id INTEGER NOT NULL, period_id TEXT NOT NULL, doc BLOB NOT NULL, PRIMARY KEY(user_id,period_id));
CREATE TABLE IF NOT EXISTS node_usage (user_id INTEGER NOT NULL, node_id TEXT NOT NULL, period_id TEXT NOT NULL, rate_revision TEXT NOT NULL, rate_milli INTEGER NOT NULL, upload INTEGER NOT NULL, download INTEGER NOT NULL, PRIMARY KEY(user_id,node_id,period_id,rate_revision));
CREATE TABLE IF NOT EXISTS entitlement_operations (id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, doc BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS node_meter_policies (id TEXT PRIMARY KEY, doc BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS business_node_usage (site_id TEXT NOT NULL, user_id INTEGER NOT NULL, node_id TEXT NOT NULL, period_id TEXT NOT NULL, rate_revision TEXT NOT NULL, rate_milli INTEGER NOT NULL, upload INTEGER NOT NULL, download INTEGER NOT NULL, PRIMARY KEY(site_id,user_id,node_id,period_id,rate_revision));
CREATE TABLE IF NOT EXISTS business_usage_acks (user_id INTEGER NOT NULL, node_id TEXT NOT NULL, period_id TEXT NOT NULL, rate_revision TEXT NOT NULL, upload INTEGER NOT NULL, download INTEGER NOT NULL, PRIMARY KEY(user_id,node_id,period_id,rate_revision));
