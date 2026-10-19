#!/bin/sh
set -eu
umask 077
demo_hash="$(printf '%s' "$DEMO_REDIS_PASSWORD" | sha256sum)"
demo_hash="${demo_hash%% *}"
printf 'user default off\nuser demo on #%s ~demo:* +@read +@write -@dangerous +ping +info +select\n' "$demo_hash" > /data/demo-users.acl
exec /usr/local/bin/docker-entrypoint.sh redis-server \
  --aclfile /data/demo-users.acl --appendonly yes \
  --maxmemory 64mb --maxmemory-policy noeviction
