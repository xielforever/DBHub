-- Preserve binding versions after revocation; a stale administrator form must
-- never delete a later replacement binding with a reset version number.
ALTER TABLE sys_external_identities ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT true;
