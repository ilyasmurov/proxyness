## improvement
Redesigned status bar — wider window, inline Connect/Disconnect button, spinning globe indicator
Doubled window width (420 → 840) and merged the old StatusBar + full-width ConnectionButton into a single row: animated wireframe Earth with scrolling continents when connected, red outlined circle when disconnected, spinner during connect/reconnect. "Connected to <ip>" now has the IP in grey, uptime pushed to the right next to the action button.

## feature
Clickable transport badge to switch UDP/TLS/Auto on the fly
Clicking the UDP/TLS badge in the status bar opens a dropdown with all three transport modes and instantly switches — daemon setMode + forced reconnect. The old Transport submenu in Settings is gone.
