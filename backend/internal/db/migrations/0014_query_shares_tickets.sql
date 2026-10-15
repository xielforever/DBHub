CREATE TABLE sys_query_shares (
 id BIGSERIAL PRIMARY KEY,
 token_hash TEXT NOT NULL UNIQUE,
 user_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
 name VARCHAR(200) NOT NULL,
 payload JSONB NOT NULL,
 expire_at TIMESTAMPTZ NOT NULL,
 revoked BOOLEAN NOT NULL DEFAULT FALSE,
 access_count INTEGER NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_query_shares_owner ON sys_query_shares(user_id,created_at DESC);
ALTER TABLE sys_tickets DROP CONSTRAINT IF EXISTS sys_tickets_status_check;
ALTER TABLE sys_tickets ADD CONSTRAINT sys_tickets_status_check CHECK(status IN ('pending','approved','rejected','executing','executed','failed'));
ALTER TABLE sys_tickets ADD COLUMN approved_by BIGINT REFERENCES sys_users(id);
