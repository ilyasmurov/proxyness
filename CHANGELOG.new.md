## fix
Server graceful shutdown — broadcast MsgSessionClose to all UDP sessions on SIGTERM
Docker stop now gives the server 5s to send a new 0x07 MsgSessionClose packet to every active session before exiting. Daemon handles the message by closing the transport immediately, which triggers engine.healthLoop to reconnect within ~1s instead of waiting for the 20s keepalive dead-ticker. Cuts deploy downtime from ~4 minutes to ~2s for v1.24.3+ clients.

## improvement
Deploy workflow: [skip-deploy] commit-message filter
Commits with [skip-deploy] in the message (docs, changelog, backlog updates) now skip the entire build/push/deploy pipeline so unrelated changes don't restart the production server.

## improvement
Add backlog/ folder for deferred ideas
First entry: full graceful reload design (option C — SIGUSR2 fork-exec + FD inheritance + UDP session handover). Deferred until usage patterns justify the 4-7 day effort.
