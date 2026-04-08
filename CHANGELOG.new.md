## fix
Force diagnostic logger on for Windows debug cycle
Windows UAC elevation drops env vars so SMUROV_DEBUG=1 never reaches the elevated child, and --debug conflicts with Electron's legacy Node flag. Temporarily always-on until the 1.27+ Windows crash is found.
