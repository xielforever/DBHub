-- A refresh binds one immutable set of validated values, shared by every tile.
ALTER TABLE sys_dashboard_refreshes ADD COLUMN params JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(params)='object');
ALTER TABLE sys_dashboard_refresh_tiles ADD COLUMN params JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(params)='object');
