# Full graceful server reload (option C: fork-exec + FD inheritance)

## What

Replace the current `docker stop → docker run` deploy cycle with a zero-downtime binary upgrade that keeps existing TCP proxy streams and UDP sessions alive across the upgrade.

## Why

The current deploy path kills the process. Every push to `main` triggers a full restart, which drops:

- All in-flight TCP proxy connections (user might be in the middle of a download, SSH session, or WebSocket)
- All UDP sessions along with their `sessionKey`, ARQ state, and stream tables — clients have to re-handshake

The cheap v1.24.3-era fix already makes clients reconnect within 1-2 seconds, and the SIGTERM broadcast (server side) now tells them to do it immediately. That's "good enough" for ordinary use, but it still interrupts long-lived sessions. If the project grows (more paying users, live streaming, gaming traffic via UDP) we'll want true zero-downtime.

## How

The classic pattern: parent process catches `SIGUSR2`, forks-execs a new binary, **passes listening sockets to the child via inherited file descriptors**, keeps serving existing connections, and exits when it has no work left.

### Server code changes

1. Refactor `server/cmd/main.go`:

   ```go
   // On startup, check if we're the upgraded child
   if os.Getenv("GRACEFUL") == "1" {
       // FD 3 = inherited TCP listener, FD 4 = inherited UDP conn
       tcpFile := os.NewFile(3, "tcp-listener")
       ln, _ = net.FileListener(tcpFile)
       udpFile := os.NewFile(4, "udp-conn")
       udpConn, _ = net.FilePacketConn(udpFile)
   } else {
       ln, _ = net.Listen("tcp", *addr)
       udpConn, _ = net.ListenPacket("udp", *addr)
   }

   // Signal handler: SIGUSR2 → spawn child, then drain
   sigCh := make(chan os.Signal, 1)
   signal.Notify(sigCh, syscall.SIGUSR2, syscall.SIGTERM)
   go func() {
       sig := <-sigCh
       if sig == syscall.SIGUSR2 {
           spawnChild(ln, udpConn)
       }
       beginDrain()
   }()
   ```

2. Implement `spawnChild`:

   ```go
   func spawnChild(ln net.Listener, udpConn net.PacketConn) error {
       tcpFile, _ := ln.(*net.TCPListener).File()
       udpFile, _ := udpConn.(*net.UDPConn).File()
       cmd := exec.Command(os.Args[0], os.Args[1:]...)
       cmd.ExtraFiles = []*os.File{tcpFile, udpFile}
       cmd.Env = append(os.Environ(), "GRACEFUL=1")
       cmd.Stdout = os.Stdout
       cmd.Stderr = os.Stderr
       return cmd.Start()
   }
   ```

3. Implement `beginDrain`:

   - Close the listeners (child already holds them — no new connections arrive at us)
   - Track active TCP connections with a `sync.WaitGroup`
   - Wait with a hard timeout (e.g. 5 min) then force-close remaining
   - For UDP: harder. See "UDP handover" below.

### UDP session handover

UDP is the tricky part. The parent owns the session map in memory; the child starts empty. Options:

1. **Dump-and-load.** Parent writes `sessions.gob` to disk just before forking, child loads it on startup. Races are possible because the child starts reading the shared UDP socket immediately — we need to gate the socket read loop behind a "sessions loaded" signal.

2. **Live IPC over a pipe.** Pass a third FD with a streaming protobuf of session entries. Parent streams, child loads, then child signals "ready" back, parent begins drain. More robust, more code.

3. **Shared state store (Redis, shared memory).** Biggest architectural change, probably overkill.

Option 1 is simpler and probably sufficient. Serialize `ClientAddr` (bytes), `Token`, `SessionKey`, `DeviceID`, `LastSeen`, and the ARQ state. Skip `streams` — let them be re-established lazily if the client still has them.

ARQ state is the hardest bit. `RecvBuffer` + `SendBuffer` have sequence numbers, retransmit timers, and a pktNum counter. If we serialize them right and the child resumes, the client won't notice. Getting this wrong means dropped packets or dup'd data during handover.

### Deploy pipeline changes

The existing deploy pipeline does `docker pull + docker run`. For graceful reload we need to **update the binary inside the running container**.

Options:

1. **Shared volume with binaries, SIGUSR2 to container PID 1.**

   Deploy:
   ```bash
   docker cp ./new-binary proxyness:/app/proxy-server.new
   docker exec proxyness sh -c 'mv /app/proxy-server.new /app/proxy-server && kill -USR2 1'
   ```

   Breaks if base image changes (glibc, system packages).

2. **SO_REUSEPORT + two containers + health check switchover.** Kernel distributes connections to both. Less code inside the server but more orchestration on the host.

3. **Front proxy (haproxy/traefik) on host routing to two backend containers.** Most flexible, biggest architectural change. Adds a hop to every connection.

Option 1 is the path of least resistance. Start there.

## Cost

Honest estimates:

| Step | Effort |
|---|---|
| Signal handler, context, tracked connections | ½ day |
| TCP drain + timeout | ½ day |
| FD inheritance plumbing (TCP+UDP) | ½ day |
| UDP session dump-and-load (serialization) | 1 day |
| ARQ state handover + tests | 1-2 days |
| Deploy pipeline rewrite | ½ day |
| Hardening / load testing / bug hunting | 1-2 days |

**Total: 4-7 working days.** Solo. Not counting unforeseen bugs.

## Blockers

- Clients must be on a version that survives brief packet loss during the handover — current v1.24.3 is fine because it has fast reconnect.
- Need to instrument the server enough that we can *observe* what happens during a reload (metrics on handover time, packet loss, drain duration). Without that we're debugging blind.
- Should only attempt once the cheap broadcast-close is in production and stable.

## Related

- `server/cmd/main.go` — needs full rewrite of startup/shutdown
- `server/internal/udp/session.go` — sessions to serialize
- `pkg/udp/arq/` — ARQ controller state
- `.github/workflows/deploy.yml` — deploy pipeline

## Why it's deferred

The cheap SIGTERM + broadcast-close + client fast-reconnect combo (landed in v1.24.3) already reduces the deploy blip to ~1-2 seconds for most users. Going from "1-2 seconds" to "zero seconds" costs an order of magnitude more effort and carries new risks (dropped packets during handover, complicated failure modes). Revisit when:

- We have paying users with SLA requirements
- Gaming / real-time UDP traffic becomes a primary use case
- Deploys become frequent enough that even 2 seconds × N/day adds up
