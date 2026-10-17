-- Immutable driver artifacts and template revisions. State may be disabled, never rebound.
CREATE TABLE sys_jdbc_drivers (
 id BIGSERIAL PRIMARY KEY,
 artifact_id VARCHAR(120) NOT NULL UNIQUE,
 name VARCHAR(100) NOT NULL,
 version VARCHAR(80) NOT NULL,
 class_name VARCHAR(255) NOT NULL,
 sha256 CHAR(64) NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT true,
 created_by BIGINT NOT NULL REFERENCES sys_users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE sys_datasource_templates (
 id BIGSERIAL PRIMARY KEY,
 template_key VARCHAR(80) NOT NULL,
 revision INTEGER NOT NULL CHECK(revision > 0),
 name VARCHAR(100) NOT NULL,
 product VARCHAR(80) NOT NULL,
 driver_id BIGINT NOT NULL REFERENCES sys_jdbc_drivers(id),
 dialect VARCHAR(20) NOT NULL DEFAULT '' CHECK(dialect IN ('','mysql','postgres')),
 url_template TEXT NOT NULL,
 fields JSONB NOT NULL CHECK(jsonb_typeof(fields)='array'),
 enabled BOOLEAN NOT NULL DEFAULT true,
 created_by BIGINT NOT NULL REFERENCES sys_users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(template_key, revision)
);
CREATE INDEX idx_datasource_template_driver ON sys_datasource_templates(driver_id);
ALTER TABLE sys_connections DROP CONSTRAINT sys_connections_type_check;
ALTER TABLE sys_connections ADD CONSTRAINT sys_connections_type_check CHECK(type IN ('mysql','postgres','redis','mongo','jdbc'));
ALTER TABLE sys_connections ADD COLUMN template_revision_id BIGINT REFERENCES sys_datasource_templates(id);
ALTER TABLE sys_connections ADD COLUMN parameters JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(parameters)='object');
ALTER TABLE sys_connections ADD CONSTRAINT connection_template_kind CHECK((type='jdbc')=(template_revision_id IS NOT NULL));
CREATE INDEX idx_connection_template ON sys_connections(template_revision_id);
ALTER TABLE sys_meta_snapshots ADD COLUMN metadata_status JSONB NOT NULL DEFAULT '{}'::jsonb;
