# Architecture

The service combines a Go/Echo web application, SQLite persistence and an
authoritative BIND server in one container. The deployment script creates zones
from `DDNS_DOMAINS`; record changes go through `nsupdate` against local BIND.

## Request paths

- `/@/` and its subroutes provide the authenticated administrator interface.
- `/update`, `/nic/update`, `/v2/update` and `/v3/update` retain device HTTP Basic
  authentication. They update A or AAAA according to the submitted address family.
- Administrator sessions are separate from per-host device credentials.
- Google and WebAuthn routes use one configured HTTPS origin; Google identities
  must match the exact configured email allowlist.

## Components

| Directory | Responsibility |
|---|---|
| `dyndns/handler` | HTTP handlers, sessions, trusted proxies, security dashboard, Google and passkey authentication |
| `dyndns/model` | Host, legacy alias, record, activity and authentication models |
| `dyndns/nswrapper` | BIND update operations and address handling |
| `dyndns/records` | Validation and transactional static record management |
| `dyndns/certificates` | ACME DNS-01 provider, encrypted certificate storage, readiness checks and renewal jobs |
| `dyndns/views` | Server-rendered administrator pages |
| `dyndns/static` | Frontend assets, filtering/sorting and certificate status polling |
| `deployment` | Docker image, Compose example and BIND initialization |
| `tests` | Disposable integration fixtures and polling regression checks |

## DNS data

Hosts own dynamic A/AAAA addresses. Static record sets support A, AAAA, CNAME,
TXT, MX, SRV, CAA and delegated NS records. The editor displays existing legacy
CNAMEs alongside static records without changing storage on read. An explicit
save converts a legacy alias into a static record within the same database
transaction; DNS update failures roll back the database changes.

The DNS editor rejects conflicts with dynamic hosts and incompatible record
types. BIND updates are atomic at the DNS operation level. SQLite and BIND do not
share a distributed transaction, so a database commit failure after a successful
DNS update still requires reconciliation.

## Certificates

ACME uses DNS-01 challenges for names in configured zones. The provider writes
challenge TXT values through the record layer, waits for local authoritative DNS
readiness and removes challenge values afterward. Dedicated host certificates and
zone wildcard/apex certificates are separate resources. Host package links never
substitute a wildcard certificate.

A background worker handles issuance and renewal. Browser polling shows pending
work and refreshes after a terminal result. Private keys are encrypted at rest;
keep the certificate encryption key and database together in backups. Package
and PEM downloads require an authenticated HTTPS administrator session.

## Authentication and proxy boundaries

`DDNS_SESSION_SECRET` must contain at least 32 bytes. Configure
`DDNS_TRUSTED_PROXIES` narrowly; forwarding headers from untrusted peers are
ignored. The administrator interface supports password login, optional Google
sign-in and passkeys. Device Basic authentication remains compatible with HTTP
clients that cannot use HTTPS.

Failed authentication records include the requested hostname where available.
Only failed administrator login attempts count toward automatic IP blocking.
Attempted passwords are intentionally retained in the protected security log;
see the README for retention and access considerations.

## Persistence and upgrades

Persist `/root/database` and `/var/cache/bind`. Back up SQLite consistently,
including its encryption key, before upgrades. Startup migrations preserve
existing records and credentials. Existing installations must supply the session
secret; Google/passkey configuration is optional.

## Validation and CI

Run `go build ./...`, `go test ./...` and `go test -race ./...` from `dyndns`.
`tests/m0_smoke.py` exercises the disposable Docker/BIND deployment;
`tests/features_smoke.py` adds Pebble-backed ACME validation. Never run automated
certificate tests against the production CA. `tests/certificates_poll_test.js`
checks terminal-state polling and error handling.

`build.yml` validates branch pushes and pull requests without duplicate tag runs.
`BuildEmAll.yml` is the sole publisher. It uses the upstream `DOCKER_USERNAME` and
`DOCKER_PASSWORD` secrets and defaults to `bbaerthlein/docker-ddns-server`; set the
`DOCKER_IMAGE_NAME` repository variable to publish elsewhere. It builds four
platforms and publishes release/latest tags on matching master changes or manual
dispatch. Publishing credentials are never required by the Go test workflow.

## Unified activity and live lists (v2.2.2)

`handler/activity.go` merges update outcomes, API authentication failures and optional
nginx failures, suppresses matching proxy duplicates, and collapses consecutive identical
results per hostname while retaining IP/result transitions and repeat counts.
`handler/proxy_logs.go` incrementally imports a read-only `DDNS_NGINX_LOG_DIR`, persists
inode/offset/anchor checkpoints, handles rotation/truncation and gzip archives, and
retains only parsed update metadata (never raw URLs or authorization headers).

`static/js/live-lists.js` supplies client pagination, column filters, sorting, and partial
HTML refreshes shared by Hosts, Records, Certificates, Logs and Security. Draft forms
pause refreshes. Pages use normal document scrolling, with only the existing top
navigation/logo remaining sticky. Lists have no separate vertical scroll container
and their column headings scroll with the page. The original database history remains intact; collapsing is a view.
