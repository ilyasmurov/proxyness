## fix
Reentrancy guard in client startReconnect
The previous guard checked the React `reconnecting` state, which is async — when the polling effect and a transport-error effect both fired in the same tick, both saw `reconnecting=false`, both passed, both spawned independent retry loops. Result: multiple parallel /tun/start calls hitting the daemon back-to-back, each triggering "engine already active, restarting" and rebuilding gVisor stack and NAT tables. Now uses a synchronous ref so only one retry loop ever runs.

## improvement
Goroutine stack trace on engine restart
Engine.Start now logs a stack trace when it has to stop a still-active engine before starting. Makes runaway client retry loops immediately diagnosable instead of guessing from the daemon log.
