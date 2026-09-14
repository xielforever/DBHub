-- 0002_roles_users.sql
-- 角色与用户（RBAC 基础表）

CREATE TABLE sys_roles (
    id          SERIAL PRIMARY KEY,
    code        VARCHAR(50)  NOT NULL UNIQUE,           -- admin / developer / readonly
    name        VARCHAR(100) NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE sys_users (
    id            BIGSERIAL PRIMARY KEY,
    username      VARCHAR(64)  NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,                 -- PBKDF2-SHA256 编码字符串
    email         VARCHAR(255),
    role_id       INTEGER REFERENCES sys_roles(id),
    is_active     BOOLEAN      NOT NULL DEFAULT TRUE,
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TRIGGER trg_sys_users_updated
    BEFORE UPDATE ON sys_users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- 内置角色
INSERT INTO sys_roles (code, name, description) VALUES
    ('admin',     '管理员',   '拥有平台全部权限'),
    ('developer', '开发者',   '可管理连接、执行 SQL'),
    ('readonly',  '只读用户', '仅可查看连接与只读查询')
ON CONFLICT (code) DO NOTHING;
