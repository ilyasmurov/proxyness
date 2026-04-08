## fix
Windows startup crash since 1.27 — loader-to-main window handoff
Create the main window before destroying the loader. The old order destroyed the loader first, which synchronously fired window-all-closed (zero windows for one tick) and on Windows that handler quits the app — the subsequent createWindow ran inside an already-quitting process and the main UI never appeared.

## improvement
Diagnostic crash logger behind --trace flag
Launch with --trace (or SMUROV_DEBUG=1 on macOS) to enable boot traces and crash capture to ~/Desktop/smurov-crash.log for future troubleshooting.
