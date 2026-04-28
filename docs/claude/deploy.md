# Deployment

All deploys are tag-triggered. Push to main does NOT deploy anything. Tag conventions:

| Component | Tag pattern | Example | Workflow | Version file |
|-----------|-------------|---------|----------|-------------|
| Server + admin | `server-*` | `server-1.0.0` | `deploy.yml` | `server/VERSION` |
| Landing page | `landing-*` | `landing-1.0.0` | `deploy-landing.yml` | `landing/VERSION` |
| Config service | `config-*` | `config-1.0.0` | `deploy-config.yml` | `config/VERSION` |
| Admin dashboard | `admin-*` | `admin-1.0.0` | `deploy-admin.yml` | `admin/VERSION` |
| Client apps | `v*` | `v1.36.0` | `release.yml` | `client/package.json` |
| Client (pre-release) | `v*-beta.*` | `v1.36.0-beta.1` | `release.yml` | `client/package.json` |

All workflows also support `workflow_dispatch` for manual trigger from GitHub UI.

- **Server**: Docker multi-stage build (React UI → Go binary → Alpine). Container runs with `--ulimit nofile=32768:32768`. A `server-X.Y.Z` tag deploys to Aeza only. The container runs under name `proxyness`, pulls `ghcr.io/${{ github.repository }}:latest`, exposes 443 TCP + UDP via the SNI router (TCP on host port 4430, UDP direct on 8443), and reads the Postgres URL from `/etc/proxyness/db.env` via `docker run --env-file`.
- **Client**: CI injects version from git tag into `package.json` before building (`v1.31.0-beta.1` → `1.31.0-beta.1`), so `package.json` always stays at the base version. Beta tags create pre-releases; stable tags create latest releases.
- **Config service**: Volume: `proxyness-config-data:/data`. Container: `proxyness-config`. Runs on Aeza only.
- **SSL**: `scripts/setup-ssl.sh` manages Let's Encrypt certs for `proxyness.smurov.com` (Aeza). The cert is not verified client-side (`InsecureSkipVerify: true` in TCP fallback; UDP transport does its own X25519+HMAC crypto and never sees TLS), so a domain/IP mismatch doesn't break connectivity.
- **VPS**:
  - **Aeza NL** (Amsterdam) — 95.181.162.242. 4 CPU, 8 GB RAM, 1 Gbps. **Bad peering to many EU hosts**: direct `curl` from this VPS to leaseweb DE throttles at 0.55 MB/s (native), which propagates to VPN goodput. CF is peered fine. Volume: `proxyness-data`.
  - **Decommissioned Timeweb NL (2026-04-28)**: a second VPS at 82.97.246.65 ran the same proxy container behind a `server-picker` UI in the client (App.tsx had `SERVERS = [aeza, timeweb]`) until the rental was dropped. The shared infra (WG `wgpn0` 10.88.0.0/24, Postgres replica access from 10.88.0.2, `-peer` SSE proxy on Aeza, sequential `deploy-aeza` → `deploy-timeweb` workflow, smurov-proxy-data volume on Timeweb) is gone. Don't re-introduce a multi-VPS topology without re-reviewing all of those — the picker, deploy.yml peer flag, admin SSE merging, and Timeweb's port-8443-because-Caddy quirk all had to be removed in lockstep.

## Shared infra on Aeza

- **Host nginx** as SNI-based TCP router on port 443. `stream` block peeks SNI hostname: `admin.proxyness.smurov.com` → 127.0.0.1:8444 (TLS terminated by nginx `http` block, Let's Encrypt cert, proxied to admin container on 8081); everything else → 127.0.0.1:4430 (proxy container, handles its own TLS). UDP 443 bypasses nginx (stream is TCP-only). Config: `/etc/nginx/nginx.conf`. Cert renewal: certbot with pre/post hooks to stop/start nginx.
- **Admin dashboard**: nginx container on Aeza only (`proxyness-admin`, port 8081:80). Deployed via `admin-*` tags. Image built locally on Aeza from `admin/Dockerfile` (until GHCR CI is wired).
- **Postgres 16** (pgdg repo) on Aeza only. `listen_addresses = '127.0.0.1,172.17.0.1'` — host loopback for `psql` and tests, plus the docker bridge gateway so the proxy container can reach the host DB from inside the bridge network. NOT public. Cluster `16/main`, restart with `systemctl restart postgresql@16-main` (`listen_addresses` requires restart, not reload). `pg_hba.conf` allows `127.0.0.1/32` and `172.17.0.0/16` via `scram-sha-256`. The proxy container's connection URL lives in `/etc/proxyness/db.env` (mode 600) as `PROXYNESS_DB_URL=postgres://proxyness:<pw>@172.17.0.1:5432/proxyness?sslmode=disable` — **do NOT** use `127.0.0.1` there, that's the container's own loopback, not the host. Reading the env file is one-shot at `docker run --env-file ...` time; if you edit `db.env`, you have to `docker rm` and `docker run` the proxy container, **not** `docker restart` (the latter keeps the old env). Schema source of truth: `server/internal/db/pg/schema.sql` — apply as `proxyness` role (use `SET ROLE proxyness` before `\i schema.sql` if connecting as `postgres`) so tables end up owned by the app role, not `postgres`.
- **One-off SQLite→Postgres migrator** (`server/cmd/migrate-pg/`): copies a SQLite snapshot into Postgres via `pgx.CopyFrom`, re-aligns sequences, filters orphan FK rows, supports `--truncate`. Ran once at cutover (2026-04-16, server-2.0.0); proxy runtime does not use it.
- **Role `proxyness` has `CREATEDB`**: granted for `server/internal/db/db_test.go` which spins a private `proxyness_test_<ns>` per test, applies `pg/schema.sql`, drops at cleanup. `CREATEDB` does NOT grant access to other databases — safe. Tests skip if `PROXYNESS_TEST_DB_URL` is not set (CI does not run these; local dev uses `ssh -L 15432:127.0.0.1:5432 root@aeza` to expose Postgres).
