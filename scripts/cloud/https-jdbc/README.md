# Opt-in HTTPS JDBC startup

This is a deployment-only adapter. It does not replace JDBC globally or change
business code, schema or UI. Default setup/start are unchanged when disabled.
Use the deployment commit based on latest main, not an older application snapshot.

## Official cloud environment configuration

Configure these non-secret variables in the reviewed environment UI:

```
DBHUB_HTTPS_JDBC_ENABLED=1
DBHUB_HTTPS_JDBC_ENDPOINT=https://dbhub-demo-wsl.tail1184b4.ts.net
```

Bind `DBHUB_DEMO_PG_PASSWORD` as a restricted **direct variable**, preserving its
exact UTF-8 value. Keep the official exact HTTP host rule, inherited HTTPS proxy,
CA verification and configured VPN. Runtime skill/environment_status must confirm
the rule enforced and binding ready; the adapter cannot query that control plane.
Do not use raw TCP, install Tailscale, change policy or bypass the proxy.

Install: `./scripts/cloud/setup.sh`
Start/recovery: `./scripts/cloud/start.sh`

These existing entrypoints automatically select the adapter when enabled. No
manual bootstrap or imported /workspace/work package is required. Publishing the
reviewed environment version with these commands/variables is an operator action;
committing this branch does not automatically update other environments.

Setup keeps dependency installation, tests and builds in Docker. The router uses
only Python standard library and the digest-pinned Python 3.12 image. Only reviewed
Dockerfile/router.py enter its build context. Start validates configuration before
any mutation, creates private state outside the checkout, starts existing default
infrastructure by container ID, builds the router, starts only router/backend/
frontend with --no-deps, waits for readiness and reconciles personal API resources.
The latest main's managed metadata backup startup is retained.

## Boundaries and credentials

Only the exact sentinel `jdbc:postgresql://wsl-demo-postgres:5432/dbhub_demo` for
`demo_reader`, database `dbhub_demo`, `ssl_mode=disable` maps to the fixed HTTPS
service and its internal postgres:5432 target. Other targets and default sessions
remain on http://jdbc:8090. Artifacts/template validation use that default runner.
`ssl_mode=disable` covers only the local demo container's PG hop, not HTTPS.
Remote HMAC bearer is derived in router memory using the exact password and message
`DBHub WSL JDBC HTTPS v1`; no token is stored in connection parameters or images.
No redirects, TCP forwarding, generalized proxying or mutation retries are used.
Known protocol errors preserve kind/SQLSTATE/definite while raw error messages are
redacted. An uncertain result stops startup; the next invocation lists and
reconciles canonical resources instead of replaying creation blindly.

`DBHUB_STATE_DIR` defaults to /workspace/dbhub-cloud (0700), outside the repository.
Private files are exclusive 0600 creations: unchanged original runtime.env backup,
https-jdbc.credentials.json (mounted read-only), and registration receipt/locks.
A previous jdbc-router.env is checked for exact credential consistency if present,
never replaced. Raw password bytes including Unicode and quotes round-trip through
JSON; no .env interpolation is used for the new password binding. Keep all private
state out of logs, chat, git and image contexts. Do not run scripts under tracing.
The long-running router is fixed at UID/GID 65532:65532, including when the host
runs as root. Its only secret mount is https-jdbc-router.credentials.json, 0600
owned by 65532. A short-lived root helper (network=none, read-only root filesystem,
only CHOWN/DAC_OVERRIDE) mounts only the canonical and dedicated credential files;
it compares contents and changes only the dedicated file's owner, never runtime.env.
Host-side canonical state remains readable by its owner for drift checks. State
paths and ancestors are rejected if symlinks before resolve or any file creation.
The API/health helpers are short-lived task-UID containers without published ports;
the API helper needs private file access/receipt writes and is not the router.

Empty original development target allowlist stays empty. Nonempty allowlists keep
all entries and gain only wsl-demo-postgres:5432. They remain enforced by the
application. Native/default JDBC services and connections are never replaced.
This adapter targets the repository's Cloud development Compose; it does not relax
production policy or silently adopt another Compose configuration.

## Canonical personal API resources

Startup logs in using the existing private generated administrator binding; JWTs
exist only in process memory. It creates or safely reuses a private owner-scoped
PostgreSQL 42.7.7 driver (locked SHA256), a private immutable template keyed
`wsl-https-postgres-v1`, and the personal `WSL local PostgreSQL via HTTPS` connection.
No other user, connection or permissions are changed. All matching names, target
parameters, artifact identity, visibility and owner must match; ambiguity, altered
metadata, same-name conflicts, stale receipt IDs or credential drift fail closed.
There is no PUT/PATCH/DELETE path. A saved connection is tested with its stored
credentials; the supplied password is never used to overwrite them.

A private local receipt binds the operator's account and canonical IDs. If a
resource later disappears, startup stops rather than silently recreating it.
A failed first creation may be safely discovered by canonical identity on restart.
Older shared catalog entries are not silently converted to personal resources:
resolve collisions explicitly with the operator. Existing old instances need not
be schema-migrated to test this deployment: use an independent worktree/project.

## Disable / rollback

Set DBHUB_HTTPS_JDBC_ENABLED=0 in the environment for subsequent default starts.
To switch an active project immediately, execute
`./scripts/cloud/https-jdbc/rollback.sh` (same Docker approval path). It restores
only backend's base endpoint and stops the router by project/service label; all
application resources, credentials, default infrastructure and volumes remain.
The sentinel connection is unavailable while disabled. Re-enable and run the
normal start.sh to recover; matching IDs are reused. No compose down, cleanup,
force-recreate of dependencies, password rotation or resource deletion is used.

For isolated verification only, set a separate DBHUB_COMPOSE_PROJECT,
DBHUB_STATE_DIR and DBHUB_BUILD_ROOT. DBHUB_HTTPS_JDBC_COMPOSE_EXTRA can point to an
operator-reviewed overlay replacing frontend/backend loopback ports. It is not
needed in the official fresh environment. The default dev.sh remains a base-only
maintenance wrapper; use start.sh to keep an enabled adapter active.

Tests: `./scripts/cloud/https-jdbc/test.sh` runs offline, nonroot Docker tests for
routing, error fidelity, configuration, exact credential persistence/drift,
resource reconciliation, default dispatch and preservation of existing dependency
containers. Synthetic executable entrypoint fixtures live only in limited tmpfs.
