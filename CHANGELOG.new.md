## fix
Config service reverse proxy uses Docker bridge gateway
Proxy container in bridge mode couldn't reach config on 127.0.0.1:8443. Now configurable via -config flag, deploy uses 172.17.0.1:8443.
