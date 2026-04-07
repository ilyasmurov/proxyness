## fix
Deploy workflow: pass commit message via env var to avoid bash parse error on multi-line messages
Previous commit directly interpolated the commit message into a bash expression, which broke when the message had newlines. Using an env var fixes that and also closes a shell injection vector.
