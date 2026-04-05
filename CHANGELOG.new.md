## fix
Re-enable CUBIC cwnd reduction on loss to prevent congestion collapse
cwnd grew to 1024 and never decreased, overwhelming ISP buffers. Now reduces 20% per loss event (max once per 500ms) so CUBIC finds equilibrium.
