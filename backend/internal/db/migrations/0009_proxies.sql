-- 0009_proxies.sql
-- 代理管理（替代 SSH 隧道演进，支持 HTTP/SOCKS5/数据库代理占位）
-- SSH 隧道表 sys_ssh_tunnels 保留兼容，标记废弃，未来版本迁移至此表

CREATE TABLE sys_proxies (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
    name        VARCHAR(128) NOT NULL,
    type        VARCHAR(32)  NOT NULL DEFAULT 'http' CHECK (type IN ('http', 'https', 'socks5', 'db_proxy', 'custom')),
    host        VARCHAR(255) NOT NULL,
    port        INTEGER NOT NULL,
    username    VARCHAR(128),
    password    TEXT,                                     -- AES-256-GCM 加密
    description VARCHAR(512) NOT NULL DEFAULT '',
    status      VARCHAR(16)  NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_proxies_user ON sys_proxies(user_id);
CREATE INDEX idx_proxies_type ON sys_proxies(type);

CREATE TRIGGER trg_sys_proxies_updated
    BEFORE UPDATE ON sys_proxies
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- 兼容：给 connections 增加 proxy_id（占位，暂不强制）
ALTER TABLE sys_connections ADD COLUMN IF NOT EXISTS proxy_id BIGINT REFERENCES sys_proxies(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_connections_proxy ON sys_connections(proxy_id);

COMMENT ON TABLE sys_proxies IS '代理配置，替代 SSH 隧道，支持 HTTP/SOCKS5/DB Proxy，占位 M5';
COMMENT ON COLUMN sys_proxies.type IS 'http|https|socks5|db_proxy|custom';
