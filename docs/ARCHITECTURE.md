# docker-ddns-server — Architecture & Engineering Handoff

**Repository:** `w3K-one/docker-ddns-server` (local: `/Users/ApoCaLypSe/Documents/@/GitHub/docker-ddns-server`)
**Image:** `w3kllc/ddns` (Docker Hub) · **Default branch:** `master` · **Owner:** w3K LLC
**Stack:** Go 1.22 · Echo v4.12 · GORM v1.25 + SQLite (mattn/go-sqlite3, CGO) · gorilla/sessions · BIND9 + `nsupdate` inside one Debian 12 container
**Snapshot:** baseline `7983a3f` ("v2.0.0"); sections 5–7 and the M0 status verified and updated 2026-10-19.

Sections 1–4 retain the pre-M0 code map and line references as historical context; sections 5–7 describe the hardened implementation. Section 12 distinguishes resolved M0 issues from remaining work.

This document is meant to be enough to extend the project without re-reading the codebase. All claims were checked against the source;
references are `path:line` relative to the repo root (Go sources live under `dyndns/`). Where `README.md` or the untracked `CLAUDE.md` disagree with the code, **the code wins**; the discrepancies are listed in section 12.

---

## Table of contents

1. Purpose & lineage
2. Runtime architecture
3. Code map and route table
4. Data model
5. Request flows
6. Configuration (environment variables)
7. Build, CI, release, tests
8. Live production deployment (operator-supplied facts)
9. Roadmap / extension spec
   - A. Additional record types and a JSON API
   - B. Integrated Let's Encrypt certificate management
10. Operational runbook
11. Conventions for contributors
12. Known bugs, risks and doc discrepancies (reference list)

---

## 1. Purpose & lineage

A self-hosted **dynamic DNS service**: routers, NVRs and cron scripts call a DynDNS-compatible HTTP endpoint (`/nic/update` and friends) with
per-host Basic-Auth credentials. The Go app then rewrites the A/AAAA record in an **authoritative BIND9 zone** that runs in the same container.
An admin web UI (`/@/…`) manages hosts, CNAMEs, update logs and a security dashboard.

Lineage:

| Generation | Repo | What it contributed |
|---|---|---|
| 1 | `dprandzioch/docker-ddns` | The original idea: BIND + nsupdate + a small HTTP update API in Docker. This repo is "inspired by" it, not a git fork of it (README.md:8). |
| 2 | `benjaminbear/docker-ddns-server` (git remote `upstream`) | The Go/Echo rewrite with a web UI, GORM/SQLite, hosts and CNAMEs, logs, log clearing, wildcard option. Upstream history goes up to `6cc20a6` (2024-05-28). w3K had already sent PR #20 upstream (`1386132`, from `w3K-dot-co/patch-2`). |
| 3 | `w3K-one/docker-ddns-server` (remote `origin`) | One commit, `7983a3f` "v2.0.0" (author `w3K`, 2025-10-11), on top of upstream master. |

What w3K added in v2.0.0 (verified in code):

- **Session-based admin login** replacing Basic Auth on the UI: `/@/login` form, `/@/logout` page, gorilla cookie sessions with 24 h or 30 d ("remember me") lifetime (`dyndns/handler/auth.go:42-150`, `dyndns/handler/session.go`).
- **HTTPS awareness** for the admin panel: detects TLS or proxy headers and redirects `/@/*` to https when a proxy header shows HTTPS is available (`dyndns/handler/auth.go:196-224`, `dyndns/handler/middleware.go:78-108`).
- **Failed-auth logging and IP blocking**: every failed login or update auth is stored with the attempted username **and password** (an intentional threat-intel decision, see 11.4). Three admin-panel failures within 72 h block the IP for 7 days. Blocked IPs are redirected to `http://127.0.0.1` on **every** route (`dyndns/handler/security.go`, `dyndns/handler/middleware.go:14-48`).
- **Security dashboard**: active blocks, recent failures, unblock action (`dyndns/handler/security_dashboard.go`, `dyndns/views/security_dashboard.html`, `blocked_ips.html`, `failed_auths.html`).
- **Branding knobs**: `DDNS_TITLE` (default changed to "w3K DynDNS"), `DDNS_POWERED_BY`, `DDNS_POWERED_BY_URL`, `DDNS_LOGOUT_URL`. A logo at `static/icons/logo.{png,webp,svg}` is picked up automatically (`dyndns/handler/handler.go:125-168`).
- **Lowercase normalization**: new and updated hosts are lowercased, and legacy rows are lowercased on the fly in `ListHosts` (`dyndns/handler/host.go:40-133`).
- **Module path** changed to `github.com/w3K-one/docker-ddns-server/dyndns` (`dyndns/go.mod:1`).
- **CI**: `BuildEmAll.yml` builds and pushes a multi-arch image (amd64/386/armv7/arm64) and creates a GitHub release (`.github/workflows/BuildEmAll.yml`).
- Image renamed to `w3kllc/ddns` (`deployment/docker-compose.yml:4`).

---

## 2. Runtime architecture

### 2.1 Process model

One container runs **two long-lived processes** under one `sh -c` with no supervisor (`deployment/Dockerfile:28`):

```
CMD ["sh", "-c", "/root/setup.sh ; service named start ; /root/dyndns"]
```

1. `/root/setup.sh` runs once. It generates BIND config and zone files from the env. Failures are **ignored** because of `;`: if `DDNS_DOMAINS` is missing, setup exits 1 but named and the app still start (`deployment/setup.sh:4-6`).
2. `service named start` starts BIND via the Debian init script. named daemonizes and runs as user `bind`. **Nothing restarts named if it dies**, and its logs go to syslog, and no syslog daemon runs in the container, so named's own logs are effectively lost. Use `rndc` (section 10).
3. `/root/dyndns` is the Go binary, listening on `:8080` (`dyndns/main.go:160`). It is the last command of the shell. `sh` is PID 1 and does not forward SIGTERM, so `docker stop` waits for the 10 s timeout and then SIGKILLs both.

Working directory is `/root` (`deployment/Dockerfile:22`). The app relies on **relative paths** from there:

| Relative path in code | Absolute in container | Source |
|---|---|---|
| `database/ddns.db` | `/root/database/ddns.db` (volume) | `dyndns/handler/handler.go:197-204` |
| `views/` | `/root/views` | `dyndns/main.go:28`, `deployment/Dockerfile:24` |
| `static/` | `/root/static` | `dyndns/main.go:61`, `deployment/Dockerfile:25` |
| `static/icons/logo.*` | logo detection | `dyndns/handler/handler.go:131-139` |

The app writes DNS by running `/usr/bin/nsupdate` (from the `dnsutils` package) against `server localhost` (`dyndns/nswrapper/update.go:25,40`).

### 2.2 Diagram

```
                 Internet / LAN clients
   (routers, NVRs, UniFi, curl)        resolvers worldwide
            |  HTTP :8080 (prod 35080)        |  DNS :53 udp/tcp
            v                                 v
 +-----------------------------------------------------------------+
 |  container "ddns"   (debian:12-slim, WORKDIR /root)             |
 |                                                                 |
 |  /root/dyndns  (Go, Echo :8080)        named (BIND9, user bind) |
 |  +---------------------------+         +----------------------+ |
 |  | global mw: Logger,        |         | zones from setup.sh: | |
 |  |  IPBlocker, Cleanup       |         |  zone "<d>" {        | |
 |  | /@/*  session UI          |         |   type master;       | |
 |  | /update,/nic/update,      |         |   file "<d>.zone";   | |
 |  |  /v2/update,/v3/update    | nsupdate|   allow-update       | |
 |  |   BasicAuth (per host)    |-------->|     { localhost; };  | |
 |  | /ping                     | (exec,  |  }                   | |
 |  +-------------+-------------+ no TSIG)| recursion no;        | |
 |                |                       +----------+-----------+ |
 |                v                                  v             |
 |   /root/database/ddns.db (SQLite)   /var/cache/bind/<d>.zone    |
 |        [volume ddns_db]             + <d>.zone.jnl              |
 |                                     [volume ddns_bind]          |
 +-----------------------------------------------------------------+
   /etc/bind/named.conf is regenerated on every new container (not on a volume)
```

```mermaid
sequenceDiagram
  participant R as Router
  participant E as Echo (Go)
  participant DB as SQLite
  participant N as nsupdate
  participant B as BIND (named)
  R->>E: GET /nic/update?hostname=toledo.ddns.ws&myip=1.2.3.4 (Basic auth)
  E->>DB: IsIPBlocked(clientIP)
  E->>DB: SELECT host WHERE user,pass,hostname,domain
  E->>N: temp file: server localhost / zone / update delete / update add / send
  N->>B: RFC2136 UPDATE (from 127.0.0.1, allowed by allow-update {localhost;})
  B-->>N: NOERROR (journal .jnl appended)
  E->>DB: save host.Ip, insert Log
  E-->>R: 200 "good\n"
```

### 2.3 `setup.sh` behaviour (`deployment/setup.sh`)

- Requires `DDNS_DOMAINS`, `DDNS_PARENT_NS`, `DDNS_DEFAULT_TTL` (lines 4-6). `DDNS_ADMIN_LOGIN` is not required (the check is commented out, line 3).
- `DDNS_IP=$(curl icanhazip.com)` (line 8). This is the public IP of the host at first zone creation. **No `-4` flag**: on a host with IPv6 egress it can return an IPv6 address, which is then written as an `A` record and breaks zone loading.
- For each domain `d` in `DDNS_DOMAINS` (commas replaced by spaces, line 10):
  - If `/etc/bind/named.conf` has no `zone "d"` stanza, the script appends one (lines 12-24):
    ```
    zone "d" { type master; file "d.zone"; allow-query { any; }; allow-transfer { none; }; allow-update { localhost; }; };
    ```
    `named.conf` lives in the image layer, so this is redone for every new container. Zones are **only** the ones in `DDNS_DOMAINS`. A zone file on the volume for a removed domain (e.g. the stale `w3k.cx.zone` in production) is simply not loaded.
  - If `/var/cache/bind/d.zone` does not exist, the script creates it **once** (lines 26-44). Existing zone files are never touched again, so later changes to `DDNS_PARENT_NS` or `DDNS_DEFAULT_TTL` do **not** apply to existing zones.
- Fixes permissions on every start (lines 48-51): `root:bind 770` on the dir, `bind:bind 644` on every file (including `.jnl`).

Zone template, exactly as rendered (`deployment/setup.sh:30-43`):

```
$ORIGIN .
$TTL 86400	; 1 day
<d>		IN SOA	<DDNS_PARENT_NS>. root.<d>. (
				74         ; serial
				3600       ; refresh (1 hour)
				900        ; retry (15 minutes)
				604800     ; expire (1 week)
				86400      ; minimum (1 day)
				)
			NS	<DDNS_PARENT_NS>.
			A	<DDNS_IP>
$ORIGIN <d>.
$TTL <DDNS_DEFAULT_TTL>
```

Implications:
- **Single NS** (the parent NS name). SOA MNAME is that NS and RNAME is `root.<d>.`. The serial starts at 74 and BIND increments it on each dynamic update.
- The apex `A` record has a TTL of 86400 and is not managed by the app.
- After the first dynamic update, BIND keeps changes in `<d>.zone.jnl`. It periodically re-dumps the zone file, rewriting it in canonical form; the pretty template disappears. Never hand-edit a zone that has a journal unless it is frozen (section 10.4).

### 2.4 BIND options (`deployment/named.conf.options`)

`directory "/var/cache/bind"; dnssec-validation auto; recursion no; allow-transfer { none; }; auth-nxdomain no; listen-on-v6 { any; };`

The server is authoritative-only. There is no response-rate limiting (`rate-limit {}`), no `minimal-responses`, no logging channel and no DNSSEC signing.

### 2.5 How nsupdate is authorized

- The app writes a temp script and runs `nsupdate <file>` (`dyndns/nswrapper/update.go:17-45`). The script has `server localhost` (line 25) and `zone <domain>` (line 26) and uses **no `-k` key / TSIG**.
- BIND accepts it because of `allow-update { localhost; };`. In BIND the `localhost` ACL matches **all addresses of the container's own interfaces** (127.0.0.1, ::1 and the container's bridge IP), not only loopback.
- So any process that can run code in the container, or in its network namespace, can change the zones. This includes `docker exec ddns nsupdate` from the host, which is exactly what the out-of-band acme.sh hook relies on (section 8).
- Success is detected by **exit code 0 and empty stdout**: any stdout output is treated as an error (`dyndns/nswrapper/update.go:46-52`).

---

## 3. Code map

```
dyndns/
  main.go                 Echo setup, renderer, middleware order, all routes
  go.mod                  module github.com/w3K-one/docker-ddns-server/dyndns, go 1.22   (go.sum is .gitignored!)
  handler/
    handler.go            Handler struct, env parsing, DB init/migrate, update auth, log-clear trigger
    auth.go               login/logout, IsAuthenticated, IsHTTPS, CSRF token generator (unused)
    session.go            gorilla CookieStore wrapper, session secret
    middleware.go         IPBlocker, SessionAuth, HTTPSRedirect, UpdateAuth, Cleanup, ExtractIPFromRequest
    security.go           failed-auth logging, block/unblock, cleanup, queries
    security_dashboard.go security UI handlers
    host.go               host CRUD, legacy lowercase migration, UpdateIP (the DynDNS endpoint)
    cname.go              CNAME list/add/delete
    log.go                update-log views, ClearLogs
  model/
    host.go  cname.go  log.go  failed_auth.go (FailedAuth + BlockedIP)
  nswrapper/
    update.go             UpdateRecord / DeleteRecord -> nsupdate
    ip.go                 GetIPType, GetCallerIP, private ranges, ShrinkUserAgent
  ipparser/
    ipparser.go           ValidIP4 / ValidIP6
    ipparser_test.go      the only tests in the repo
  views/                  goview templates (master layout + pages)
  static/                 bootstrap 4, jQuery 3.4.1, jQuery UI, actions-1.0.0.js, icons
deployment/  Dockerfile, setup.sh, named.conf.options, docker-compose.yml, envfile
.github/workflows/  build.yml, BuildEmAll.yml
```

### 3.1 `dyndns/main.go`

- `e.Logger.SetLevel(log.ERROR)` (line 22) only affects Echo's own logger. Package-level `gommon/log` calls used throughout the handlers use the **global** gommon logger, which defaults to INFO (gommon v0.4.2 `log.go:62`). So `log.Info` lines **are printed**.
- `middleware.Logger()` (line 24) writes one JSON access log line per request, with the URI (including the query string).
- Renderer is goview through echoview: root `views`, master `layouts/master`, `.html`, **`DisableCache: true`** (templates are re-parsed on every render). Template funcs are `year`, `hasPrefix`, `slice`, `mod` (lines 27-55).
- Validator is go-playground v10 (line 58). Static files are served from `/static` (line 61).
- Order: `InitDB` (67), then `ParseEnvs` (72; it also creates the session store), then global `IPBlockerMiddleware` (78), then `CleanupMiddleware` (81).

Echo specifics that matter: `Group.Use` only applies to routes registered **after** the call. It also registers catch-all `RouteNotFound` routes for the group (`echo@v4.12.0/group.go:22-33`). That is why `/@/login` and `/@/logout`, registered at lines 95-99 before `Use(SessionAuth)` at 103, stay public.

### 3.2 Full route table

Global middleware on every request, in order: `middleware.Logger` → `IPBlockerMiddleware` → `CleanupMiddleware`.

| Method | Path | Handler | Group middleware | Auth |
|---|---|---|---|---|
| GET | `/` | inline: 301 → `/@/` (main.go:84-86) | none | none |
| GET | `/@/login` | `ShowLoginPage` (auth.go:23) | HTTPSRedirect (skips `/@/login`) | none |
| POST | `/@/login` | `HandleLogin` (auth.go:42) | HTTPSRedirect (skips) | credentials in form |
| GET | `/@/logout` | `HandleLogout` (auth.go:111) | HTTPSRedirect | none |
| GET | `/@/` | `ListHosts` (host.go:40) | HTTPSRedirect + SessionAuth* | session |
| GET | `/@/hosts` | `ListHosts` | same | session |
| GET | `/@/hosts/add` | `AddHost` (host.go:136) | same | session |
| GET | `/@/hosts/edit/:id` | `EditHost` (host.go:148) | same | session |
| POST | `/@/hosts/add` | `CreateHost` (host.go:173) | same | session |
| POST | `/@/hosts/edit/:id` | `UpdateHost` (host.go:213) | same | session |
| GET | `/@/hosts/delete/:id` | `DeleteHost` (host.go:259) — **state change via GET** | same | session |
| GET | `/@/cnames` | `ListCNames` (cname.go:15) | same | session |
| GET | `/@/cnames/add` | `AddCName` (cname.go:33) | same | session |
| POST | `/@/cnames/add` | `CreateCName` (cname.go:52) | same | session |
| GET | `/@/cnames/delete/:id` | `DeleteCName` (cname.go:86) — **GET** | same | session |
| GET | `/@/logs` | `ShowLogs` (log.go:23) | same | session |
| GET | `/@/logs/host/:id` | `ShowHostLogs` (log.go:39) | same | session |
| GET | `/@/security` | `ShowSecurityDashboard` (security_dashboard.go:11) | same | session; **only registered if `DDNS_ADMIN_LOGIN` set** (main.go:126) |
| GET | `/@/security/blocked-ips` | `ShowBlockedIPs` (security_dashboard.go:45) | same | same |
| GET | `/@/security/failed-auths` | `ShowFailedAuths` (security_dashboard.go:62) | same | same |
| POST | `/@/security/unblock/:ip` | `UnblockIPHandler` (security_dashboard.go:80) | same | same |
| GET | `/update` | `UpdateIP` (host.go:299) | UpdateAuth | per-host Basic |
| GET | `/nic/update` | `UpdateIP` | UpdateAuth | per-host Basic |
| GET | `/v2/update` | `UpdateIP` | UpdateAuth | per-host Basic |
| GET | `/v3/update` | `UpdateIP` | UpdateAuth | per-host Basic |
| GET | `/ping` | inline: `{"message":"OK"}` (main.go:152-157) | none | none |
| GET | `/static/*` | Echo static (main.go:61) | none | none |

\* SessionAuth is only attached when `DDNS_ADMIN_LOGIN` is non-empty (main.go:102-104). Even then, `SessionAuthMiddleware` and `IsAuthenticated` short-circuit when `DisableAdminAuth` is true (middleware.go:55, auth.go:154).

Notes:
- Only GET is registered for the update endpoints; POST returns 405.
- Unrouted handlers: `GetHost` (host.go:24, it returns the id, not the host) and `AuthenticateAdmin` (handler.go:80) are not wired to any route. `GetClientIP` (security.go:151) is a stub. `GenerateCSRFToken` (auth.go:186) is never called.
- `/@` without a trailing slash matches only the group catch-all and returns 404.

### 3.3 `dyndns/handler/handler.go`

- `Handler` struct (21-35): `DB`, `AuthAdmin` (mutable shared flag, effectively unused), `Config Envs{AdminLogin, Domains}`, `Title`, `DisableAdminAuth`, `LastClearedLogs`, `ClearInterval`, `AllowWildcard`, `LogoutUrl`, `LogoPath`, `SessionStore`, `PoweredBy`, `PoweredByUrl`.
- `CustomValidator` (42-49) wraps `validator.Struct`. `Error{Message}` (51-53) is the JSON error body used everywhere.
- `AuthenticateUpdate` (57-79): see flow 5a. It calls `CheckClearInterval()` first (58), so **log clearing is only triggered by update calls**.
- `authByEnv` (95-108): parses `DDNS_ADMIN_LOGIN` as an htpasswd document (`tg123/go-htpasswd`, DefaultSystems: bcrypt, apr1/md5, sha, crypt, …) and checks it with `Match`.
- `ParseEnvs` (113-193): reads all env vars (section 6) and calls `InitSessionStore` (188).
- `InitDB` (196-219): creates `database/` with mode `os.ModePerm`, opens `database/ddns.db`, and AutoMigrates `Host, CName, Log, FailedAuth, BlockedIP`.
- `CheckClearInterval` / `DateEqual` (222-235): if `LastClearedLogs` is set and is not today, the app starts `ClearLogs` in a goroutine. There is no locking.

### 3.4 `dyndns/handler/auth.go`, `session.go`, `middleware.go`, `security.go`, `security_dashboard.go`

See flow 5d for the behaviour. Key functions:

- `HandleLogin` (auth.go:42-108). `HandleLogout` (111-150) always renders `logout.html`; the template does the delayed JS redirect to `DDNS_LOGOUT_URL`. `IsAuthenticated` (153-178). `IsHTTPS` (196-218) returns true for TLS, `X-Forwarded-Proto: https`, `X-Forwarded-Ssl: on` or `X-Url-Scheme: https`.
- `InitSessionStore` (session.go:23-46): `sessions.NewCookieStore(secret)` with **only a hash key, so the cookie is signed but not encrypted**. Options: Path `/`, MaxAge 24 h, HttpOnly, SameSite **Strict**.
- `GetSessionSecret` (session.go:59-72): `DDNS_SESSION_SECRET`, or else `AdminLogin + "-session-secret-key"`.
- `ExtractIPFromRequest` (middleware.go:179-207): `X-Real-IP` → **leftmost** `X-Forwarded-For` → `RemoteAddr`. It is used for blocking and failed-auth logging. **Headers are trusted unconditionally.**
- `security.go` constants (12-16): `MaxFailedAttempts=3`, `BlockDuration=168h`, `LookbackPeriod=72h`.

### 3.5 `dyndns/handler/host.go`, `cname.go`, `log.go`

- `ListHosts` (40-133) lowercases `Hostname` and `UserName` in a transaction. On a hostname collision it appends `1`, `2`, … (no dash). This runs on **every** page view; nothing records that a migration already happened. It renders `listhosts` with `migrationReport`. Renamed hosts are **not** re-pushed to BIND.
- `CreateHost` (173-208), `UpdateHost` (213-255), `DeleteHost` (259-294), `UpdateIP` (299-373), `checkUniqueHostname` (375-397). See flows 5a and 5b.
- `cname.go`: `ListCNames` preloads `Target`. `AddCName` lists all hosts in a dropdown. `CreateCName` (52-82). `DeleteCName` (86-113).
- `log.go`: `CreateLogEntry` (14). `ShowLogs` shows the **last 30** logs (25). `ShowHostLogs` shows the last 30 for one host (46). `ClearLogs` (59-64) runs raw SQL `DELETE FROM LOGS WHERE created_at < datetime('now','-N day');REINDEX LOGS;`.

### 3.6 `dyndns/nswrapper`

- `UpdateRecord(hostname, target, addrType, zone, ttl, enableWildcard)` (update.go:14-55) writes:
  ```
  server localhost
  zone <zone>
  update delete <hostname>.<zone> <TYPE>
  [update delete *.<hostname>.<zone> <TYPE>]          # if wildcard
  update add <hostname>.<zone> <ttl> <TYPE> <target>
  [update add *.<hostname>.<zone> <ttl> <TYPE> <target>]
  send
  ```
  The whole script is one UPDATE message, so it is atomic. Names have no trailing dot; nsupdate treats them as absolute because no `origin` is set. There is **no escaping or validation** of `hostname` or `target`. A newline in either would inject extra nsupdate commands.
- `DeleteRecord(hostname, zone, enableWildcard)` (58-95) runs `update delete <name>` with **no type**, which removes **all RRsets** at the name, and also at `*.<name>` if wildcard is on.
- `GetIPType` (ip.go:15-23) returns `A` if `To4()!=nil`, else `AAAA` if parseable, else `""`. IPv4-mapped IPv6 (`::ffff:1.2.3.4`) is classified as `A` but its text is passed to nsupdate unchanged, so the update fails.
- `GetCallerIP` (ip.go:27-45) scans `X-Real-Ip`, then `X-Forwarded-For`, **right to left**, and returns the first address that is global unicast and not in `privateRanges` (69-94: 10/8, 100.64/10, 172.16/12, 192.0.0/24, 192.168/16, 198.18/15). The range check uses an exclusive end, and `isPrivateSubnet` only checks IPv4 (97-109), so IPv6 ULA `fc00::/7` is **not** skipped. **Line 28 logs the entire request header map at INFO, including `Authorization: Basic …` (the host's credentials), on every update.**
- `ShrinkUserAgent` (48-52) keeps the first space-separated token.

### 3.7 `dyndns/ipparser`

`ValidIP4` (net.ParseIP + To4), `ValidIP6` (net.ParseIP + To16; also true for IPv4). Tests: three `ValidIP4` cases (`ipparser_test.go:7-29`).

### 3.8 Views and front-end

- `views/layouts/master.html`: Bootstrap 4 nav (Hosts, CNames, Logs, 🔒 Security, ⏏️ Logout), logo or title, footer `© poweredBy year`. **Every page render must pass `title`, `logoPath`, `poweredBy`, `poweredByUrl`** (lowercase keys) or the layout renders blanks.
- Pages: `listhosts`, `edithost` (add and edit; TTL dropdown fixed to 20/60/3600/14400, so `DDNS_DEFAULT_TTL` is not used by the UI), `listcnames`, `addcname`, `listlogs`, `login`, `logout`, `security_dashboard`, `blocked_ips`, `failed_auths`, `index_sample` (unused).
- `static/js/actions-1.0.0.js`: all mutations are jQuery AJAX. The edit and add forms (`#editHostForm`) are serialized and POSTed to `/@/<hosts|cnames>/<add|edit>/<id>`. Deletes are **GET**. Errors are shown with `alert(JSON.message)`. Navigation highlighting assumes a `.nav-<segment>` element exists (line 154). A new top-level page needs a nav item with class `nav-<path>`, or the script throws.
- `listhosts.html:30-31` renders every host's **plaintext DDNS password** into a hidden div, used by the "copy URL" button. That button builds `proto://user:pass@<zone>/update?hostname=…` (actions js:117), which is wrong when the UI runs on a non-default port or host (production: 35080).
- `listhosts.html:24` hardcodes the suffix `MEZ` on timestamps.

---

## 4. Data model

All models embed `gorm.Model` (`ID uint` PK autoincrement, `CreatedAt`, `UpdatedAt`, `DeletedAt` soft-delete index). Table names are GORM defaults: `hosts`, `c_names`, `logs`, `failed_auths`, `blocked_ips`. **Migrations are only `AutoMigrate`** (handler.go:210-216). There is no versioned migration table.

### 4.1 `Host` (`dyndns/model/host.go:10-19`)

| Field | Type | GORM | form | validate | Notes |
|---|---|---|---|---|---|
| Hostname | string | `unique_index:idx_host_domain;not null` | hostname | `required,min=1` | Label(s) left of the zone. No charset validation. |
| Domain | string | `unique_index:idx_host_domain;not null` | domain | `required,fqdn` | **Not checked against `DDNS_DOMAINS`.** |
| Ip | string | | ip | `omitempty,ipv4\|ipv6` | Last pushed IP. |
| Ttl | int | | ttl | `required,min=20,max=86400` | |
| LastUpdate | time.Time | | lastupdate | | |
| UserName | string | `not null` | username | `min=1` | Not unique; lowercased. |
| Password | string | | password | `min=6` | **Plaintext**, compared in SQL. |

`unique_index:` is GORM **v1** tag syntax. GORM v2 ignores it, so **no unique index exists**. This was verified: a fresh DB has only `idx_hosts_deleted_at`, and inserting a duplicate `(hostname, domain)` succeeds. Uniqueness depends only on `checkUniqueHostname`.

`(*Host).UpdateHost(u)` (22-36) copies `Ip, Ttl, UserName, Password`. It returns `true`, which forces a DNS push, only if `Ip` or `Ttl` changed.

### 4.2 `CName` (`dyndns/model/cname.go:8-14`)

| Field | Type | validate | Notes |
|---|---|---|---|
| Hostname | string (`not null`, form `hostname`) | `required,min=1` | Owner label inside **the target's** domain. |
| Target | `Host` (belongs-to) | `required` | validator v10 also validates the nested `Host` struct, so a target whose stored fields fail Host validation (e.g. password < 6) blocks CNAME creation. |
| TargetID | uint | | FK to hosts.id, read from form `target_id`. |
| Ttl | int (form `ttl`) | `required,min=20,max=86400` | |

### 4.3 `Log` (`dyndns/model/log.go:10-20`)

`Status bool, Message, Host Host (belongs-to), HostID, SentIP, CallerIP, TimeStamp, UserAgent`. One row is written per update attempt that passed auth.

### 4.4 `FailedAuth` (`dyndns/model/failed_auth.go:12-20`)

`IPAddress (index, not null), UserAgent, Timestamp (index), Path, Username, Password` (attempted password, stored intentionally).

### 4.5 `BlockedIP` (`dyndns/model/failed_auth.go:23-40`)

`IPAddress (uniqueIndex, not null), BlockedAt (index), BlockedUntil (index), FailureCount, IsPermanent, LastAttemptAt, Reason`. `IsBlocked()` returns `IsPermanent || now < BlockedUntil`. Nothing in the code sets `IsPermanent=true`.

### 4.6 Startup and "normalization"

- `InitDB` → `AutoMigrate` (adds tables and columns; never drops; does not create the intended unique index).
- Hostname and username lowercasing happens lazily in `ListHosts` (4.1 above), not at startup.
- Soft delete: hosts and CNAMEs are removed with `Unscoped().Delete` (hard delete). Logs and failed auths use soft-delete-aware `Delete` (rows get `deleted_at`). `ClearLogs` uses raw SQL (hard delete).

---

## 5. Request flows

### 5a. DynDNS update API

1. GET `/update`, `/nic/update`, `/v2/update`, `/v3/update` share `UpdateAuthMiddleware` and `UpdateIP`; HTTP remains supported (`dyndns/main.go:135`).
2. Global blocking uses the same trusted-proxy IP extraction as authentication, DNS caller-IP fallback and access logging (`dyndns/handler/middleware.go:13`, `dyndns/nswrapper/proxy.go:57`, `dyndns/main.go:77`). A blocked client receives 302 to `http://127.0.0.1`, including on API routes. Headers from untrusted socket peers are ignored. For trusted peers, XFF is walked right-to-left to the first untrusted hop; X-Real-IP is used only if XFF is absent. Private addresses are retained rather than skipping trust boundaries.
3. The request-driven cleanup middleware launches expired-block and old-failure cleanup at most hourly (`dyndns/handler/middleware.go:148`). Failed-auth retention is hardcoded to 30 days (`dyndns/handler/security.go:178`).
4. Missing/malformed Basic Auth returns `401 badauth\n` without recording a failure. Explicitly wrong credentials are recorded, but API failures do not count toward blocks (`dyndns/handler/middleware.go:107`, `dyndns/handler/security.go:36`). Database errors are returned separately and do not become bad-credential records.
5. `AuthenticateUpdate` rejects empty credentials and empty hostname/domain components, lowercases username and hostname, and matches all four fields using parameterized SQL. Password matching remains case-sensitive. The host is stored as `updateHost` (`dyndns/handler/handler.go:62`). SQL statement logging is disabled at database initialization so bound credentials cannot leak (`dyndns/handler/handler.go:219`).
6. `UpdateIP` builds a log, checks the authenticated FQDN, chooses valid `myip` or the trusted client IP, and calls `UpdateRecord`. Explicit private `myip` remains accepted. IPv4 maps to A and IPv6 to AAAA (`dyndns/handler/host.go:300`, `dyndns/nswrapper/ip.go:14`).
7. DNS failure returns `400 dnserr`; success updates `Host.Ip`/`LastUpdate`, writes a log, and returns `200 good`. Every call rewrites DNS; there is no `nochg` shortcut (`dyndns/handler/host.go:351`). Other responses include `badauth`, `badrequest`, and `notfqdn`.

### 5b. nsupdate wrapper

`UpdateRecord` writes a temporary script and runs `/usr/bin/nsupdate <file>` (`dyndns/nswrapper/update.go:15`):

```text
server localhost
zone <domain>
update delete <host>.<domain> <TYPE>
[update delete *.<host>.<domain> <TYPE>]
update add <host>.<domain> <ttl> <TYPE> <target>
[update add *.<host>.<domain> <ttl> <TYPE> <target>]
send
```

Wildcard commands are conditional. BIND authorizes local updates without TSIG (`deployment/setup.sh:21`). Updates replace one value per RRset; arbitrary rdata still lacks escaping and formatting. Those changes belong to Feature A (`dyndns/nswrapper/update.go:27`).

`DeleteRecords` sends all supplied names in one single-zone update, including wildcard names when enabled. Names containing command separators/whitespace are rejected; process errors and output become errors (`dyndns/nswrapper/update.go:64`, `dyndns/nswrapper/update.go:81`). It still deletes all RR types at a name, matching the legacy API. Disabling the wildcard flag can still leave old wildcard records.

### 5c. Host CRUD

Routes are at `dyndns/main.go:108`. Create lowercases hostname and username, validates model fields and checks host/CNAME collisions, then inserts the host. If an IP is supplied, DNS is written immediately; without one it waits for an update (`dyndns/handler/host.go:173`). Credentials are form values; the UI's optional Generate buttons use JavaScript (`dyndns/views/edithost.html:49`, `dyndns/static/js/actions-1.0.0.js:164`). The domain selector lists configured domains, but the server only validates FQDN syntax, **not membership in DDNS_DOMAINS** (`dyndns/views/edithost.html:15`, `dyndns/model/host.go:13`).

Edit updates IP, TTL and credentials, with a DNS write when IP/TTL changes (`dyndns/handler/host.go:213`, `dyndns/model/host.go:22`). Create/edit still save the row before DNS and do not compensate for DNS failure.

Delete loads dependent CNAMEs and removes the host, logs and CNAME rows in a database transaction. It deletes the host and aliases from BIND together before committing. Callback failures return actual errors and roll back SQL; HTTP responses are emitted outside the transaction (`dyndns/handler/host.go:259`, `dyndns/handler/dns.go:5`). SQLite and BIND cannot share a distributed transaction: a database commit failure after successful DNS deletion still requires reconciliation.

The composite unique index is installed at startup after deterministic deduplication: keep the lowest-ID active row (otherwise lowest ID), reassign CNAME/log references, remove duplicate rows, then AutoMigrate. The survivor retains its credentials and IP. Migration is transactional and idempotent; it makes no DNS changes (`dyndns/model/host.go:12`, `dyndns/handler/migration.go:11`). Back up the database before deploying this migration.

### 5d. CNAME CRUD

A CNAME inherits its zone from an existing target Host; there is no independent domain field or arbitrary external target (`dyndns/model/cname.go:8`). Creation parses a positive numeric `target_id`, loads it with a bound SQL parameter, validates, checks collisions, inserts, then immediately writes DNS (`dyndns/handler/cname.go:52`). Deletion rolls back the database on DNS errors (`dyndns/handler/cname.go:91`). Routes remain GET for deletion (`dyndns/main.go:121`).

### 5e. Admin auth, sessions and IP blocking

- `/@/login` checks the htpasswd document using the library's default systems (not bcrypt only), creates a signed cookie for 24 hours or 30 days with remember-me (`dyndns/handler/handler.go:105`, `dyndns/handler/auth.go:42`).
- `DDNS_SESSION_SECRET` is mandatory, minimum 32 bytes; there is no derived fallback. Cookies are signed, not encrypted, with HttpOnly and SameSite Strict. Secure is set for direct TLS or HTTPS reported by a trusted proxy (`dyndns/handler/session.go:24`, `dyndns/handler/auth.go:192`). The same configured key supports sessions across restarts.
- HTTPS redirects apply only to admin routes, skip login, and depend on a trusted peer supplying X-Forwarded-Proto (`dyndns/handler/middleware.go:73`). API HTTP compatibility is preserved.
- Failed passwords remain in the database intentionally. Three admin failures in 72 hours trigger a 168-hour block asynchronously. API failures are excluded from the count (`dyndns/handler/security.go:12`, `dyndns/handler/security.go:21`, `dyndns/handler/security.go:50`).
- Security pages and unblock actions are registered only when admin login is configured (`dyndns/main.go:128`, `dyndns/handler/security_dashboard.go:11`). Without admin credentials, admin session protection remains disabled; the session secret is still required.

### 5f. Log cleanup

`DDNS_CLEAR_LOG_INTERVAL` is a retention age in days, not a timer period. A positive value initializes `LastClearedLogs`; the first update-auth call on a later calendar day launches raw-SQL log deletion in a goroutine (`dyndns/handler/handler.go:180`, `dyndns/handler/handler.go:230`, `dyndns/handler/log.go:59`). No traffic means no cleanup. Existing cleanup timestamp synchronization is outside M0.

---

## 6. Configuration (environment variables)

| Variable | Required/default | Actual consumer and behavior |
|---|---|---|
| `DDNS_ADMIN_LOGIN` | Optional; empty disables admin auth | htpasswd document; `dyndns/handler/handler.go:127`, `dyndns/handler/handler.go:105`, `dyndns/main.go:104`. |
| `DDNS_DOMAINS` | Required by setup | Comma-separated zones (`deployment/setup.sh:4`, `deployment/setup.sh:10`); UI domain choices (`dyndns/handler/handler.go:192`). Empty-value validation in Go remains ineffective; server-side managed-zone membership is not checked. |
| `DDNS_PARENT_NS` | Required by setup | SOA MNAME and NS for newly created zone files (`deployment/setup.sh:5`, `deployment/setup.sh:32`, `deployment/setup.sh:39`). |
| `DDNS_DEFAULT_TTL` | Required by setup | Initial zone-file default TTL (`deployment/setup.sh:6`, `deployment/setup.sh:42`). Go does not read it; host TTL is submitted by the form (`dyndns/model/host.go:15`, `dyndns/views/edithost.html:33`). |
| `DDNS_SESSION_SECRET` | Required; at least 32 bytes | Cookie signing key (`dyndns/handler/session.go:26`). Use a cryptographically random secret, e.g. `openssl rand -hex 32`; no fallback. |
| `DDNS_TRUSTED_PROXIES` | Optional; empty trusts no proxies | Comma-separated exact IPv4/IPv6 addresses or CIDRs; invalid entries fail startup (`dyndns/handler/handler.go:197`, `dyndns/nswrapper/proxy.go:13`). Applies to IP extraction, HTTPS headers and access-log client IP. Configure only the proxy peers/chains you operate. |
| `DDNS_ALLOW_WILDCARD` | Optional; false | Parsed with `strconv.ParseBool`; controls wildcard writes/deletes (`dyndns/handler/handler.go:151`, `dyndns/nswrapper/update.go:29`). |
| `DDNS_CLEAR_LOG_INTERVAL` | Optional; 0 disables | Retention days for update logs (`dyndns/handler/handler.go:180`, `dyndns/handler/log.go:59`); failed-auth cleanup separately uses 30 days. |
| `DDNS_TITLE` | Unset: `w3K DynDNS` | UI title; explicitly empty stays empty (`dyndns/handler/handler.go:135`). |
| `DDNS_LOGOUT_URL` | Empty | Destination passed to logout template (`dyndns/handler/handler.go:158`, `dyndns/handler/auth.go:136`). |
| `DDNS_POWERED_BY` | Empty/unset: `w3K LLC` | Footer label (`dyndns/handler/handler.go:166`). |
| `DDNS_POWERED_BY_URL` | Empty/unset: `https://w3K.one/` | Footer URL (`dyndns/handler/handler.go:173`). |
| `TZ` | Runtime/platform default | No explicit Go or setup consumer, and not set in the supplied image/Compose file. An operator-supplied timezone depends on runtime zoneinfo availability; do not assume image support solely from the variable. |

---

## 7. Build, CI, release, tests

- The Go module is in `dyndns/go.mod:1`, with committed `dyndns/go.sum` (already tracked at baseline despite the old ignore rule, which M0 removes). The Docker builder uses Go 1.22, downloads locked dependencies, then builds and tests **all packages** with `-mod=readonly` (`deployment/Dockerfile:1`, `deployment/Dockerfile:8`). CGO is enabled by the Go builder default for SQLite.
- Runtime is Debian 12 plus BIND9, dnsutils and curl (`deployment/Dockerfile:12`). The copied BIND options file is explicitly mode 0644 so builds from restrictive local checkouts remain readable by the bind user (`deployment/Dockerfile:21`). CMD is a shell sequence with semicolons: setup, `service named start`, Go app. It does **not** use exec or a supervisor and does not stop after setup failure (`deployment/Dockerfile:29`).
- Setup writes `/etc/bind/named.conf`, not `named.conf.local`, and creates missing zone files. Public-address discovery now forces IPv4 (`deployment/setup.sh:8`, `deployment/setup.sh:15`, `deployment/setup.sh:26`). Existing zone files are preserved.
- The image workflow triggers on master changes to `dyndns/**`, `deployment/**`, or itself, and supports manual dispatch (`.github/workflows/BuildEmAll.yml:5`). It builds **amd64, 386, arm/v7 and arm64**, pushes version/latest tags, and creates a release (`.github/workflows/BuildEmAll.yml:92`, `.github/workflows/BuildEmAll.yml:97`). Version comes from the commit message or an incremented existing tag (`.github/workflows/BuildEmAll.yml:29`).
- Separate Go CI builds/tests all packages on branch pushes only (`.github/workflows/build.yml`). Tag pushes do not duplicate the checks. The obsolete tag-triggered image publisher has been removed; `BuildEmAll.yml` is the sole image publisher.
- Local: export `DDNS_SESSION_SECRET`, then `docker compose -f deployment/docker-compose.yml up --build`. Compose now has an explicit build context and requires the secret (`deployment/docker-compose.yml:5`). Supply real admin credentials for normal use; its bundled admin hash is only a placeholder. Port mappings are 53 TCP/UDP and 8080 (`deployment/docker-compose.yml:16`). Use the configured `dyndns.example.com` zone for local API/dig checks.
- Checks: `cd dyndns && go build ./... && go test -race ./... && go vet ./...`. M0 regression suites cover authentication, target-ID SQL input, transaction rollback and DNS cascade, unique migration, secret validation, proxy boundaries and header-log suppression (`dyndns/handler/hardening_test.go:1`, `dyndns/nswrapper/hardening_test.go:1`). Existing parser tests remain (`dyndns/ipparser/ipparser_test.go:1`). Container smoke tests are in `tests/m0_smoke.py:1`.

---

## 8. Live production deployment (operator-supplied facts, 2026-10-19)

- **Host:** Furia (w3K VM; ZeroTier `10.101.0.100`). Container `ddns`, image `w3kllc/ddns:latest`.
- **Managed by Portainer via Chimera** (`https://chimera.w3k.io`). Compose project `ddns`; file on Furia: `/data/compose/3/v1/docker-compose.yml`. **All stack changes go through Chimera, never a local `docker compose`.**
- **Ports:** `53/tcp+udp` public (authoritative DNS); web UI `35080 → 8080`.
- **Env:** `DDNS_DOMAINS=ddns.ws,dvr.bz`, `DDNS_PARENT_NS=furia.w3k.co`, `DDNS_DEFAULT_TTL=600`, `DDNS_ALLOW_WILDCARD=TRUE`, `DDNS_TITLE=w3K DDNS`, `DDNS_CLEAR_LOG_INTERVAL=69`, `DDNS_POWERED_BY=w3K LLC`, `DDNS_POWERED_BY_URL=https://w3K.LLC`, `DDNS_LOGOUT_URL=https://w3K.one/`, `TZ=America/New_York`. Admin login and session secret are set (never copy them into docs or code).
- **Volumes:** `ddns_db → /root/database` (SQLite); `ddns_bind → /var/cache/bind` (`ddns.ws.zone`, `dvr.bz.zone` + `.jnl` journals). A **stale `w3k.cx.zone` + `.jnl`** also sits there although `w3k.cx` is not in `DDNS_DOMAINS`. Clean it up or document it.
- **Delegation:** both zones are delegated to `furia.w3k.co`.
- **Out-of-band ACME (built 2026-10-09; the manual workflow the new feature replaces):**
  - `acme.sh` on Furia, user `ApoCaLypSe`, `~/.acme.sh`, default CA Let's Encrypt; renewal cron `/etc/cron.d/acme-sh` (daily 05:54).
  - DNS-01 hook `~/.acme.sh/dnsapi/dns_ddnsws.sh` (copy at `Snippets-and-Scripts/Bash/dns_ddnsws.sh`) pipes `server localhost / zone ddns.ws / update add <fqdn>. 60 TXT "<val>" / send` into `sudo docker exec -i ddns nsupdate`, and the matching `update delete` on cleanup. The zone is hardcoded to `ddns.ws` via `DDNSWS_ZONE` (default), so `dvr.bz` names need `DDNSWS_ZONE=dvr.bz`.
  - Issued so far: `toledo.ddns.ws` (RSA-2048 + EC-256), `apocalypse.ddns.ws` (RSA-2048). The full chain and key were downloaded by hand and uploaded to client UniFi gateways (UniFi OS → Control Plane → Console → Certificates). That upload is still manual on every renewal.

---

## 9. Roadmap / extension spec

Ordering principle: **fix the security bugs first (M0)**. Both features enlarge the authenticated surface and reuse the update-auth code path.

### M0 — Hardening prerequisites (implemented; review before Feature A)
1. Rewrite the update-auth lookup with explicit `Where("user_name = ? AND password = ?", ...)` and reject empty user or password (12.1).
2. Stop logging request headers (12.3).
3. Fix the CNAME raw-condition lookup (12.2).
4. Use real unique indexes (`uniqueIndex`, GORM v2) plus a migration that dedupes first (12.4).
5. Make transactions return real errors so they roll back (12.5); delete CNAMEs from BIND on host delete (12.6).
6. Trust proxy headers only from a configured `DDNS_TRUSTED_PROXIES` list (12.8); require `DDNS_SESSION_SECRET` or generate and persist one (12.9).
7. Fix the Dockerfile so it runs `go test ./...` and commits `go.sum` (12.11); use `curl -4` in setup.sh (12.12); include `deployment/**` in the CI paths (12.13).

**Acceptance:** legacy HTTP update routes and deliberate failed-password storage retained; regression tests cover 12.1, 12.2 and 12.5 plus migration/proxy/session behavior. Configure a 32+ byte session secret and explicit trusted proxy list before rollout. No production deployment is part of M0.

### Feature A — Additional record types + JSON API

**Model (recommended: one generic table, not per-type models).**
```go
type Record struct {
  gorm.Model
  Domain   string `gorm:"not null;index:idx_rr,unique"` // one of DDNS_DOMAINS
  Name     string `gorm:"not null;index:idx_rr,unique"` // relative label, "@" for apex, may start with "*."
  Type     string `gorm:"not null;index:idx_rr,unique"` // A AAAA CNAME TXT MX SRV CAA NS
  Value    string `gorm:"not null;index:idx_rr,unique"` // canonical rdata (see below)
  TTL      int    `gorm:"not null"`
  HostID   *uint  // set when the record belongs to a DDNS host (owner); nil = static/admin record
  Managed  string // "ddns" | "static" | "acme" (acme = transient TXT for challenges)
}
```
Reason: one RRset can hold multiple values (several MX/TXT/NS), so values must be rows. A single table keeps the BIND sync logic uniform. Keep `Host` (credentials + DDNS address) and `CName` for backward compatibility, and later migrate CName rows into `Record`.

**Per-type validation:**
| Type | Value format | Rules |
|---|---|---|
| A / AAAA | IP | family must match type; static records allowed without a host |
| CNAME | FQDN (external allowed) | **exclusive**: no other records at the same name; never at apex |
| TXT | free text | stored unquoted; on emit, split into ≤255-byte quoted strings and escape `"` and `\` |
| MX | `pref target` | 0–65535, target FQDN, not an IP, should not be a CNAME |
| SRV | `prio weight port target` | name must be `_svc._proto.label`; all ints 0–65535 |
| CAA | `flags tag value` | flags 0/128; tag ∈ issue, issuewild, iodef; value quoted on emit |
| NS | FQDN | subzone delegation only (never at apex, which setup.sh owns) |

**nswrapper v2.** Replace `UpdateRecord` with `ApplyRRset(zone, name, type, ttl, values []string)`:
- delete only that (name, type) RRset, then add each value, in **one nsupdate transaction**;
- add `DeleteRRset`;
- add a rdata formatter per type (proper quoting);
- read back with `dig @127.0.0.1` in tests;
- never use `update delete <name>` without a type.

**JSON API** (session or API-token auth; tokens are a new `APIToken` model with hashed secret, scope, and an optional bound host):
`GET/POST /api/v1/zones/{zone}/records`, `GET/PUT/DELETE /api/v1/zones/{zone}/records/{id}`, `PUT /api/v1/zones/{zone}/rrsets/{name}/{type}` (replace whole RRset). Host-scoped tokens may only touch names at or under their own host label (e.g. `toledo` → `toledo`, `*.toledo`, `_acme-challenge.toledo`).

**UI:** a "Records" page per zone (list, filter by type, add/edit with per-type forms), and record counts on the hosts list.

**Milestones:** A1 model, migration, nswrapper v2 + tests → A2 TXT/MX/CAA/SRV/NS via UI → A3 JSON API + tokens → A4 migrate CName rows into Record (keep the old routes as aliases).

**Acceptance:** every type round-trips (create → `dig` shows exact rdata → delete → gone); CNAME exclusivity enforced; multi-value MX/TXT works; the existing `/nic/update` behaviour is unchanged.

### Feature B — Integrated Let's Encrypt certificate management

**Recommendation: Go-native ACME with `github.com/go-acme/lego/v4`, not shelling out to acme.sh.** The server is authoritative for its zones, so a custom lego `challenge.Provider` can call nswrapper v2 directly (`ApplyRRset(zone, "_acme-challenge.<label>", "TXT", 60, [...])`). This gives typed errors, no extra runtime and no cron, and it's testable against Pebble. lego also supports ARI, revocation with reason codes, EC/RSA keys and alternate chains.

**Scope:**
- Certificates for any name in a managed zone:
  - zone wildcards: `*.ddns.ws` + `ddns.ws`, `*.dvr.bz` + `dvr.bz`;
  - per-host certificates: `toledo.ddns.ws`, optionally plus `*.toledo.ddns.ws`.
- Key type: RSA-2048 or EC-256, chosen per certificate.
- CA: LE production or staging (`DDNS_ACME_DIRECTORY`), with a staging toggle in the UI.

**Models:**
```go
type ACMEAccount struct { gorm.Model; Email string; Directory string; KeyPEMEnc []byte; RegistrationURI string }
type Certificate struct {
  gorm.Model
  Domains   string // comma list, first = CN
  KeyType   string // rsa2048|ec256
  Directory string
  HostID    *uint  // owner host (per-host cert) or nil (admin/zone cert)
  Serial, Issuer string
  NotBefore, NotAfter time.Time
  CertPEM, ChainPEM []byte   // leaf; issuer chain (fullchain = leaf+chain, without the root)
  KeyPEMEnc []byte           // AES-GCM with key from DDNS_CERT_KEY (32 bytes, env or file)
  Status    string           // pending|valid|renewing|failed|revoked|expired
  AutoRenew bool
  LastError string; LastRenewAt *time.Time; RevokedAt *time.Time; RevokeReason int
}
type CertEvent struct { gorm.Model; CertificateID uint; Action, Actor, Result string } // audit trail
```

**Operations:**
- **Issue:** create the TXT records → wait for propagation by querying the **authoritative** server (`furia.w3k.co`), not recursive resolvers → finalize → store the certificate → remove the TXT records (always, in a `defer`).
- **Auto-renew:** an in-process scheduler (hourly tick) renews when ARI says so, or at ≤30 days before expiry. Use jitter, cap concurrency at 1, back off on failure (1h → 24h), and record a `CertEvent`.
- **Manual renew-now** button and API.
- **Revoke** (reason: unspecified/keyCompromise/superseded/cessationOfOperation) → status `revoked`. **Reissue** creates a new key by default (it never reuses the key of a cert revoked for keyCompromise). "Revoke previous" is offered after a reissue.
- **Download:**
  - files: `fullchain.pem` (leaf + intermediates), `cert.pem`, `chain.pem`, `privkey.pem`, an optional `bundle.pem` (key + fullchain), and an optional `.pfx` with a one-time password;
  - Content-Disposition filenames `<cn>-fullchain.pem` / `<cn>.key` (matches what UniFi expects);
  - every download is audited.
- **Owner self-service:** `GET /cert/{cn}/fullchain.pem` and `/cert/{cn}/privkey.pem` with HTTP Basic using the **host's DDNS credentials**. Only certificates whose `HostID` matches are served, so a client gateway (e.g. a UniFi UCG on-boot/cron script) can pull its renewed certificate automatically. Add rate limiting and IP-block integration.
- **Notifications** (optional): webhook/email on failure and on renewal.

**Config:** `DDNS_ACME_EMAIL`, `DDNS_ACME_DIRECTORY` (default LE prod; `https://acme-staging-v02.api.letsencrypt.org/directory` for staging), `DDNS_CERT_KEY` (encryption key, required when certificates are enabled), `DDNS_ACME_AUTHORITATIVE_NS` (default `DDNS_PARENT_NS`), `DDNS_CERT_RENEW_DAYS` (30).

**Routes:**
- admin: `/@/certs`, `/@/certs/new`, `/@/certs/{id}`, `/@/certs/{id}/renew`, `/@/certs/{id}/revoke`, `/@/certs/{id}/download/{part}`;
- API: `/api/v1/certs` (CRUD and actions);
- owner: `/cert/{cn}/{part}`.

**Safety:**
- keys are never logged and are encrypted at rest; DB file mode 0600;
- downloads only over HTTPS (refuse plain HTTP unless `DDNS_ALLOW_INSECURE_DOWNLOAD=true` for LAN testing);
- add CAA records (`0 issue "letsencrypt.org"`, plus `issuewild`) once Feature A ships;
- respect LE limits: 50 certificates per registered domain per week, 5 duplicate certificates per week, and the failed-validation limit. Use staging for every test.
- **Note:** `ddns.ws` and `dvr.bz` are registered domains, so *all* hosts in a zone share the 50/week budget. Prefer one zone wildcard cert where possible.

**Migration from acme.sh on Furia:**
1. Ship B, then issue `*.ddns.ws`, `*.dvr.bz` and the per-host certs in the app.
2. Switch the client gateways to the owner download URL.
3. Remove `/etc/cron.d/acme-sh` and archive `~/.acme.sh`.
4. Optional: an importer that reads `~/.acme.sh/<name>/` (`fullchain.cer`, `<name>.key`) so existing certs show up until they expire.

**Testing:**
- Unit: a fake DNS provider, the scheduler with a fake clock, and an encryption round-trip.
- Integration: `docker compose` with **Pebble** + `pebble-challtestsrv` off; point lego at Pebble, let our BIND answer DNS-01, and check issue/renew/revoke/download end-to-end.
- Never hit LE production in CI.

**Milestones:** B1 models + crypto + lego provider + issue (admin UI, staging) → B2 download endpoints + audit → B3 scheduler/auto-renew + ARI → B4 revoke/reissue → B5 owner self-service endpoint + docs for UniFi pull scripts → B6 migration off acme.sh.

---

## 10. Operational runbook

- **Deploy/upgrade:** CI pushes `w3kllc/ddns:latest`. In Chimera → Furia environment → stack `ddns` → *Pull and redeploy*. Volumes persist. Check afterwards: `dig @furia.w3k.co SOA ddns.ws`, and log in to the UI on `:35080`.
- **Backup:** both volumes. Back up SQLite consistently (`sqlite3 .backup`, or stop the stack). For BIND: `docker exec ddns rndc sync -clean` before copying `/var/cache/bind`, so the `.jnl` changes are folded into the zone files.
- **Manual zone edits:** `rndc freeze <zone>` → edit the file → bump the serial → `rndc thaw <zone>`. Never edit a dynamic zone without freezing, or the journal and file diverge.
- **Inspect:** `docker exec ddns rndc zonestatus ddns.ws`; `dig @furia.w3k.co AXFR` is disabled; use `named-journalprint` on the `.jnl` to see dynamic changes.
- **Troubleshooting:**
  - `dnserr` → check that named is running inside the container (no supervisor);
  - an update publishes a private IP → the client sent a private `myip` (12 / 5a);
  - the UI is unreachable but DNS is fine → the Go process died; the container may still be "up" because named is PID-adjacent. Check `docker logs ddns`.

---

## 11. Conventions for contributors

- Go style as in the repo: Echo handlers on `*Handler`, GORM models in `model/`, BIND access only through `nswrapper/`, HTML templates in `views/` with the `layouts/master.html` layout. `gofmt` everything.
- Default branch `master`. Work on a short-lived branch, run `go test ./...`, merge to master, push. There is **no PR gate**: the operator (Andrew / w3K LLC) is the sole approver.
- **Commit messages: never add `Co-Authored-By` lines or any AI/Claude/Codex credit — in commits, code comments, docs or file headers.** All credit goes to **w3K LLC**.
- Always write the company name as **w3K**.
- Never commit secrets. Never print private keys or credentials in logs.

---

## 12. Known bugs, risks and doc discrepancies

Resolved in M0 (original issue numbers retained):

1. Empty-credential/struct-condition auth bypass: explicit four-field SQL and guards (`dyndns/handler/handler.go:62`).
2. CNAME raw SQL condition: positive numeric ID and bound lookup (`dyndns/handler/cname.go:59`).
3. Request-header logging removed (`dyndns/nswrapper/ip.go:25`); database statement logging disabled (`dyndns/handler/handler.go:219`). Intentional failed-password database storage remains.
4. GORM v2 unique index plus dedupe/remap migration (`dyndns/model/host.go:12`, `dyndns/handler/migration.go:11`).
5. Delete callbacks return real errors; DNS errors also roll back SQL (`dyndns/handler/host.go:270`, `dyndns/handler/cname.go:103`).
6. Host deletion removes related CNAMEs in one DNS batch (`dyndns/handler/host.go:270`, `dyndns/nswrapper/update.go:64`).
8. Trusted proxy allowlist gates IP and HTTPS headers (`dyndns/nswrapper/proxy.go:13`, `dyndns/handler/auth.go:192`). Global blocks still apply to API clients by design.
9. Mandatory 32+ byte session secret replaces predictable fallback (`dyndns/handler/session.go:24`). Cookies remain signed, not encrypted.
11. Docker tests all packages and uses committed sums (`deployment/Dockerfile:8`).
12. Setup forces IPv4 discovery (`deployment/setup.sh:8`).
13. Deployment changes trigger image CI (`.github/workflows/BuildEmAll.yml:7`).

Remaining/outside M0:

7. Update rdata is not escaped; deletes remove all types; disabling wildcards can leave orphans (`dyndns/nswrapper/update.go:27`, `dyndns/nswrapper/update.go:92`). Feature A owns type-specific RRsets and formatting.
10. Empty DDNS_DOMAINS check remains ineffective (`dyndns/handler/handler.go:192`).
14. Explicit private `myip` remains accepted (`dyndns/handler/host.go:336`).
15. Domain form choices are not server-side zone authorization; create/edit can leave a saved DB row when DNS fails (`dyndns/handler/host.go:173`, `dyndns/handler/host.go:213`).
16. No distributed atomic commit exists between SQLite and BIND; commit failure after a successful DNS delete needs reconciliation. Startup process supervision, GET-based destructive admin routes, and cleanup timestamp races are unchanged by this bounded milestone.

## v2.1.0 implementation update (2026-10-25)

Feature A's admin surface is implemented in `handler/features.go`, `records/store.go`
and `nswrapper/rrset.go`: eight static record types, whole-RRset edits, CNAME/legacy
ownership checks, quoted TXT/CAA data, type-specific deletion, session auth and CSRF.
Legacy Host/CName models/routes remain intact; this release does not implement the
roadmap's token JSON API or migrate CName rows. `records` rows describe static data;
legacy DDNS addresses are intentionally not duplicated into that table.

Feature B is implemented in `certificates/` with lego, encrypted ACME accounts and
private keys, asynchronous issuance, exact-value DNS-01 challenges, restart cleanup,
serial automatic zone wildcard issuance, 30-day renewal, retries, reissue, revoke,
and audited HTTPS-only downloads. ZIPs contain leaf, chain, fullchain and private key.
The user explicitly requires zone wildcard keys to remain for operator use:
`ForHost` selects only a single-SAN, unexpired production certificate matching that
host. No wildcard fallback or host-credential self-service endpoint is implemented.
ARI, PFX, outbound notifications and acme.sh migration remain outside this release.

A random encryption key persists in the existing database volume by default; an
optional hex environment key can override it. No new required startup setting is
introduced. Certificate configuration errors disable only certificate functionality.
Automation requires explicit contact-email/CA agreement setup in the admin page.
See README.md for setup, operational constraints, test commands and backup policy.

The October 25 outage was the October 19 image's mandatory session-secret check:
the deployed Chimera stack had no DDNS_SESSION_SECRET. The operator added it and
redeployed through Chimera; /ping subsequently returned OK with zero restarts.
Chimera's stack file lives on Hydra, not Furia (correcting section 8's older path note).


## Google and passkey authentication update

`handler/admin_auth.go` adds optional Google OIDC and WebAuthn using maintained
coreos/go-oidc and go-webauthn packages. `DDNS_AUTH_ORIGIN` binds WebAuthn RP ID and
origin and fixes the Google callback; Google client ID/secret and an exact email
allowlist enable Google independently. The original password and client Basic
Auth paths remain available. Build tooling now uses Go 1.27 (module minimum 1.26).

New SQLite tables: `admin_identities` (stable random WebAuthn user handle),
`admin_passkeys` (full public credential JSON, source identity, labels, use/revocation
timestamps) and `google_identities` (verified email to stable subject pins).
All configured identities map to the existing single administrator; no new roles
or public accounts exist. Source authorization is rechecked on every request,
including sessions created by a passkey. Credential revocation invalidates its
sessions. Enrollment/removal require a fresh, authenticated session and exact
request Origin. Login assertions require authenticator user verification.

The `/@/auth/*` route group enforces canonical HTTPS, POST Origin, body limits,
rate limiting and no-store responses. One-use, five-minute server-memory flows
are bound to a Secure/HttpOnly host-only cookie; registration is also bound to
the initiating admin session. Google flow state, nonce, PKCE, issuer, audience,
signature, verified email and subject pins are validated. Google callback ends
on a same-origin completion page before navigating with the Strict admin cookie.
Only the transient flow cookie is SameSite=Lax. Main access logs now use route
paths, never raw query strings. Real credential enrollment needs the operator's
own device interaction; tests use signed synthetic WebAuthn ceremonies and a
local signed OIDC issuer/token response, never real user credentials.
