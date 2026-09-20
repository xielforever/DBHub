-- 0003_ssh_tunnels.sql
-- SSH 跳板机隧道配置（私钥/口令均以 AES-256-GCM 加密后存储）

CREATE TABLE sys_ssh_tunnels (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
    name        VARCHAR(100) NOT NULL,
    host        VARCHAR(255) NOT NULL,
    port        INTEGER NOT NULL DEFAULT 22,
    username    VARCHAR(100) NOT NULL,
    auth_type   VARCHAR(20)  NOT NULL CHECK (auth_type IN ('password', 'private_key')),
    private_key TEXT,                                    -- 加密存储
    passphrase  TEXT,                                    -- 加密存储
    password    TEXT,                                    -- 加密存储
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ssh_tunnels_user ON sys_ssh_tunnels(user_id);

CREATE TRIGGER trg_sys_ssh_tunnels_updated
    BEFORE UPDATE ON sys_ssh_tunnels
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
