-- Existing definitions remain byte-for-byte compatible in their approval hash:
-- the new optional parameters field is omitted when empty.
ALTER TABLE sys_reports ADD COLUMN parameters JSONB NOT NULL DEFAULT '[]'::jsonb
 CHECK(jsonb_typeof(parameters)='array' AND jsonb_array_length(parameters)<=20);
