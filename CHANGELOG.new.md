## improvement
Remove cwnd reduction on packet loss for lossy ISP paths
Pacing prevents bursts; random UDP drops are not congestion signals. cwnd can now grow to maxCwnd=128 instead of collapsing to 32.
