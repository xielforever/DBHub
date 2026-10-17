-- Shared definitions never contain authentication material. Existing secrets remain
-- exclusively on the creator's connection; migration grants no new execution rights.
ALTER TABLE sys_jdbc_drivers ADD COLUMN visibility TEXT NOT NULL DEFAULT 'global'
 CHECK (visibility IN ('private','global'));
ALTER TABLE sys_jdbc_drivers DROP CONSTRAINT sys_jdbc_drivers_artifact_id_key;
CREATE UNIQUE INDEX jdbc_driver_global_artifact ON sys_jdbc_drivers(artifact_id) WHERE visibility='global';
CREATE UNIQUE INDEX jdbc_driver_private_artifact ON sys_jdbc_drivers(created_by,artifact_id) WHERE visibility='private';
ALTER TABLE sys_datasource_templates ADD COLUMN visibility TEXT NOT NULL DEFAULT 'global'
 CHECK (visibility IN ('private','global'));
ALTER TABLE sys_datasource_templates ADD COLUMN owner_scope BIGINT GENERATED ALWAYS AS
 (CASE WHEN visibility='global' THEN 0 ELSE created_by END) STORED;
ALTER TABLE sys_datasource_templates DROP CONSTRAINT sys_datasource_templates_template_key_revision_key;
ALTER TABLE sys_datasource_templates ADD UNIQUE (template_key,owner_scope,revision);

CREATE TABLE sys_source_definitions (
 id BIGSERIAL PRIMARY KEY,
 family_id UUID NOT NULL DEFAULT gen_random_uuid(),
 revision INTEGER NOT NULL CHECK (revision>0),
 name VARCHAR(100) NOT NULL,
 description VARCHAR(1000) NOT NULL DEFAULT '',
 owner_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
 visibility TEXT NOT NULL CHECK (visibility IN ('private','global')),
 enabled BOOLEAN NOT NULL DEFAULT true,
 config JSONB NOT NULL CHECK (jsonb_typeof(config)='object'),
 template_revision_id BIGINT GENERATED ALWAYS AS ((config->>'template_revision_id')::bigint) STORED REFERENCES sys_datasource_templates(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(family_id,revision),
 UNIQUE(id,family_id),
 CHECK (NOT (config ?| ARRAY['username','password','proxy_id','ssh_tunnel_id','token','secret']))
);
CREATE INDEX source_definition_owner ON sys_source_definitions(owner_id,visibility);
ALTER TABLE sys_connections ADD COLUMN definition_revision_id BIGINT REFERENCES sys_source_definitions(id);
ALTER TABLE sys_connections ADD COLUMN definition_family_id UUID;
CREATE UNIQUE INDEX connection_user_definition ON sys_connections(user_id,definition_family_id)
 WHERE definition_family_id IS NOT NULL;

INSERT INTO sys_source_definitions(id,revision,name,owner_id,visibility,config)
 SELECT id,1,name,user_id,'private',jsonb_build_object(
  'type',type,'host',host,'port',port,'database',COALESCE(database,''),
  'ssl_mode',ssl_mode,'connection_timeout',connection_timeout,'environment',environment,
  'template_revision_id',template_revision_id,'parameters',parameters)
 FROM sys_connections;
SELECT setval(pg_get_serial_sequence('sys_source_definitions','id'),
 COALESCE((SELECT max(id) FROM sys_source_definitions),1),
 EXISTS(SELECT 1 FROM sys_source_definitions));
UPDATE sys_connections c SET definition_revision_id=d.id,definition_family_id=d.family_id
 FROM sys_source_definitions d WHERE d.id=c.id;
ALTER TABLE sys_connections ALTER COLUMN definition_revision_id SET NOT NULL;
ALTER TABLE sys_connections ALTER COLUMN definition_family_id SET NOT NULL;
ALTER TABLE sys_connections ADD FOREIGN KEY(definition_revision_id,definition_family_id)
 REFERENCES sys_source_definitions(id,family_id);

-- Historical shared artifacts become private to their explicit creator. Public
-- result links are revoked because they predate the personal-identity boundary.
UPDATE sys_reports SET visibility='private';
UPDATE sys_dashboards SET visibility='private';
UPDATE sys_query_shares SET revoked=true;
UPDATE sys_share_tokens SET revoked=true;
-- An old shared connection is no longer a personal target for its requester.
-- Pending/approved tickets have not claimed execution and must be recreated.
UPDATE sys_tickets t SET status='rejected',execution_error='升级后请配置个人连接并重新提交工单'
 WHERE t.status IN ('pending','approved') AND NOT EXISTS
 (SELECT 1 FROM sys_connections c WHERE c.id=t.connection_id AND c.user_id=t.requester_id);
UPDATE sys_tickets SET execution_result=jsonb_build_object(
 'kind',execution_result->'kind','affected_rows',execution_result->'affected_rows',
 'duration_ms',execution_result->'duration_ms') WHERE execution_result IS NOT NULL;
