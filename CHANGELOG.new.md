## fix
Auto-update check now works on Windows — routed through system proxy via Electron net.fetch
The previous implementation used Node's fetch and https modules, neither of which respect the system proxy. On macOS the direct github.com fetch somehow squeaked through the TUN routes, but on Windows it failed with "Connection error" because GitHub is blocked at the ISP level in Russia. Switched both the version check and the installer download to Electron's net module, which uses Chromium's networking stack and routes through the SOCKS5 system proxy SmurovProxy sets up in TUN mode.
