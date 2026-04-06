## fix
UDP dead timeout too aggressive (5s → 20s)
Two lost keepalive packets would kill the session. Now tolerates up to 6 lost packets.

## fix
Landing page download links now fetch latest release from GitHub API
Direct download URLs instead of broken local file scan.
