## fix
Detect dead UDP sessions and disconnect within 5 seconds
Server now echoes keepalive packets. Client detects missing responses and auto-disconnects, fixing internet loss during server updates.
