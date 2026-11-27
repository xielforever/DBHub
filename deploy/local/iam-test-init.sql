-- Keep the shared extension outside disposable metadata test schemas. Protocol
-- functions deliberately call public.hmac and must retain that fixed boundary.
CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;
