ALTER TABLE sys_users ADD COLUMN auth_version BIGINT NOT NULL DEFAULT 0 CHECK(auth_version>=0);
-- Identity security is global; workspace runtime roles have no row policy.
CREATE TABLE sys_user_mfa (
 user_id BIGINT PRIMARY KEY REFERENCES sys_users(id) ON DELETE CASCADE,
 enabled BOOLEAN NOT NULL DEFAULT false,encrypted_secret TEXT NOT NULL DEFAULT '',
 last_counter BIGINT NOT NULL DEFAULT -1,recovery_hashes TEXT[] NOT NULL DEFAULT '{}' CHECK(cardinality(recovery_hashes)<=10),
 pending_id UUID,encrypted_pending TEXT NOT NULL DEFAULT '',pending_expires TIMESTAMPTZ,
 version BIGINT NOT NULL DEFAULT 0,updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),CHECK(NOT enabled OR encrypted_secret<>'')
);
CREATE TABLE sys_mfa_challenges (
 token_hash CHAR(64) PRIMARY KEY,user_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
 password_version CHAR(64) NOT NULL,attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
 expires_at TIMESTAMPTZ NOT NULL,used_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mfa_challenge_expiry ON sys_mfa_challenges(expires_at);
CREATE INDEX mfa_challenge_user ON sys_mfa_challenges(user_id,created_at DESC);
CREATE TABLE sys_external_identities (
 issuer TEXT NOT NULL CHECK(length(issuer)<=2048),subject TEXT NOT NULL CHECK(length(subject) BETWEEN 1 AND 255),
 user_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,version BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),PRIMARY KEY(issuer,subject),UNIQUE(issuer,user_id)
);
ALTER TABLE sys_user_mfa ENABLE ROW LEVEL SECURITY;
ALTER TABLE sys_mfa_challenges ENABLE ROW LEVEL SECURITY;
ALTER TABLE sys_external_identities ENABLE ROW LEVEL SECURITY;
