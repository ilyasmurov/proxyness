# Second exit + automatic failover — design (2026-09-15)

Tasks: [PRXNS-19](https://taskless.ru/app/smurov/tasks/PRXNS-19) (host), [PRXNS-20](https://taskless.ru/app/smurov/tasks/PRXNS-20) (client/daemon).

## Why now

Serverspace (188.227.86.205) went dark on 2026-09-15 — no ICMP, no TCP 443/22, and
their API returned nothing, from two vantage points (SkyNet SPb, Yandex Cloud). Every
client has that IP hard-coded, so the product was down until a new build shipped.
Ilya bought a second box at Aeza Amsterdam (178.236.252.28). The ask: make it a
second exit and let the client pick whichever works, on its own.

## Decisions

**Failover lives in the daemon, not the client.** The daemon already owns every
reconnect path (health-loop D1/D3, slow-poll, wake). Teaching those paths about a
list of servers gives mid-session failover for free; a client-side loop could only
help on the initial connect.

**`transport.ServerRing`** — ordered addresses, `Current/Advance/Prefer`, plus a
per-server "rejected the key" mark. `ConnectAny` walks the ring on first connect;
`tryReconnectOnce` (engine and tunnel — the single reconnect point, slow-poll
included) dials `Current()` and on failure calls `NoteFailure`:

- ENETUNREACH → stay (local link down; the route-refresh counters keep working).
- "invalid key" → mark that server rejected, move on.
- anything else → move on.

A rejection is final only when *every* server in the ring said "invalid key"
(`AllRejected`). A secondary whose device table lags the primary would otherwise
strand a freshly issued key. The reconnect loops hand back the last *retryable*
error when the rejection is not unanimous, so the client keeps trying.

**API**: `/connect` and `/tun/start` accept `servers: []string` (preferred first);
`server` stays as the legacy single address. `/status` and `/tun/status` report
`server` — the exit actually in use.

**Client**: `SERVERS` has two entries; the Settings picker is `Auto | Serverspace NL
| Aeza NL`, Auto by default. Auto sends every address with the last live one first
(`localStorage["proxyness-last-server"]`, fed from status). A manual pick sends one
address — pinned, no failover, same semantics as `transport-mode`. The persisted
choice moved to a new key (`proxyness-server-v2`): the old key only ever held
"serverspace", and honouring it would pin every install to the dead exit. Key
validation at setup asks each exit in turn. The status row shows the live exit's
label.

**Two hosts, two Postgres, one-way pull.** Each proxy uses its own local DB; a
secondary pulls `users/devices/sites/site_domains/site_ips/user_sites/changelog`
from the primary every minute over SSH (`scripts/vps/pg-sync.sh`, upsert + delete
of missing rows, children first). `traffic_stats`/`logs` stay per host. A dead
primary just freezes the secondary's snapshot — which is the failover we want; only
device management waits for the primary. `machine_id` is synced too: a binding made
on either side must hold on both.

**No backup of the Serverspace DB exists**, so Aeza starts empty: Ilya's own devices
are seeded from the local `device-key` files. When Serverspace comes back its dump is
imported first, then it becomes the secondary of Aeza (or the other way round —
one env file decides).

**AmneziaWG**: the server private key lived only on Serverspace. Aeza gets a new
server key; peers are rebuilt from the client configs mirrored on the Mac (same
client keys, PSKs, addresses, magic), so each person only re-scans a QR.

**Deploys**: all four deploy workflows run a matrix over `{serverspace: VPS_HOST /
VPS_SSH_KEY, aeza: VPS2_HOST / VPS2_SSH_KEY}` with `fail-fast: false` — a dead host
turns its leg red and never blocks the other.

## Out of scope

Admin dashboard merging stats across hosts; config-service (SQLite notifications)
replication; DNS-level failover for `proxyness.smurov.com` (landing, `/api/sync`,
config polling stay on whichever host DNS points at).
