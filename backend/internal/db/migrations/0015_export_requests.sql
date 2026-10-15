CREATE TABLE sys_export_requests (
 id BIGSERIAL PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
 connection_id BIGINT NOT NULL REFERENCES sys_connections(id) ON DELETE CASCADE,
 format VARCHAR(10) NOT NULL CHECK(format IN ('csv','json')),
 row_count INTEGER NOT NULL,
 reason TEXT NOT NULL,
 payload JSONB NOT NULL,
 status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected')),
 reviewed_by BIGINT REFERENCES sys_users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
