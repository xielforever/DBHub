-- A family's publication policy covers every published historical revision.
-- Personal database credentials stay exclusively in sys_connections.
CREATE TABLE sys_user_groups (
 id BIGSERIAL PRIMARY KEY, name VARCHAR(100) NOT NULL UNIQUE,
 description VARCHAR(500) NOT NULL DEFAULT '', version BIGINT NOT NULL DEFAULT 1,
 created_by BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE sys_user_group_members (
 group_id BIGINT NOT NULL REFERENCES sys_user_groups(id) ON DELETE CASCADE,
 user_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
 PRIMARY KEY(group_id,user_id)
);
CREATE INDEX user_group_members_user ON sys_user_group_members(user_id,group_id);
CREATE TABLE sys_source_families (
 family_id UUID PRIMARY KEY, access_mode TEXT NOT NULL DEFAULT 'global' CHECK(access_mode IN ('global','restricted')),
 access_version BIGINT NOT NULL DEFAULT 1, profile_version BIGINT NOT NULL DEFAULT 1,
 category VARCHAR(64) NOT NULL DEFAULT '', tags TEXT[] NOT NULL DEFAULT '{}' CHECK(cardinality(tags)<=10),
 maintainer VARCHAR(100) NOT NULL DEFAULT '', contact VARCHAR(200) NOT NULL DEFAULT ''
);
INSERT INTO sys_source_families(family_id) SELECT DISTINCT family_id FROM sys_source_definitions;
ALTER TABLE sys_source_definitions ADD FOREIGN KEY(family_id) REFERENCES sys_source_families(family_id);
CREATE TABLE sys_source_grants (
 family_id UUID NOT NULL REFERENCES sys_source_families(family_id) ON DELETE CASCADE,
 user_id BIGINT REFERENCES sys_users(id) ON DELETE CASCADE,
 group_id BIGINT REFERENCES sys_user_groups(id) ON DELETE CASCADE,
 CHECK(num_nonnulls(user_id,group_id)=1)
);
CREATE UNIQUE INDEX source_grant_user ON sys_source_grants(family_id,user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX source_grant_group ON sys_source_grants(family_id,group_id) WHERE group_id IS NOT NULL;
CREATE TABLE sys_source_preferences (
 user_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
 family_id UUID NOT NULL REFERENCES sys_source_families(family_id) ON DELETE CASCADE,
 favorite BOOLEAN NOT NULL DEFAULT false, last_used_at TIMESTAMPTZ,
 PRIMARY KEY(user_id,family_id)
);
CREATE INDEX source_preferences_recent ON sys_source_preferences(user_id,last_used_at DESC) WHERE last_used_at IS NOT NULL;

-- Invoker rights; no role/authorization decision is cached in Redis or JWTs.
CREATE FUNCTION dbhub_source_visible(definition_id BIGINT, actor_id BIGINT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE AS $$
 SELECT EXISTS(
 SELECT 1 FROM sys_source_definitions d JOIN sys_source_families f USING(family_id)
 JOIN sys_users u ON u.id=actor_id AND u.is_active JOIN sys_roles r ON r.id=u.role_id
 WHERE d.id=definition_id AND (
 (d.visibility='private' AND d.owner_id=actor_id) OR
 (d.visibility='global' AND (r.code='admin' OR f.access_mode='global' OR EXISTS(
 SELECT 1 FROM sys_source_grants g WHERE g.family_id=f.family_id AND
 (g.user_id=actor_id OR EXISTS(SELECT 1 FROM sys_user_group_members m WHERE m.group_id=g.group_id AND m.user_id=actor_id)))))))
 $$;
CREATE FUNCTION dbhub_cleanup_source_family() RETURNS TRIGGER LANGUAGE plpgsql AS $$
 BEGIN
 DELETE FROM sys_source_families f WHERE f.family_id=OLD.family_id
 AND NOT EXISTS(SELECT 1 FROM sys_source_definitions d WHERE d.family_id=f.family_id);
 RETURN OLD;
 END $$;
CREATE TRIGGER source_family_cleanup AFTER DELETE ON sys_source_definitions FOR EACH ROW EXECUTE FUNCTION dbhub_cleanup_source_family();

-- Immutable, closed allowlist snapshots survive connection/user deletion.
ALTER TABLE sys_audit_logs ADD COLUMN context JSONB NOT NULL DEFAULT '{}';
CREATE INDEX audit_database_account ON sys_audit_logs((context->>'database_account'),created_at DESC);
