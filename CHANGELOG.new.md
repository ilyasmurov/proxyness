## feature
Config microservice for notifications and service discovery
New smurov-config container with SQLite DB, admin UI, device key auth via proxy, background version poller.

## feature
Landing page extracted to standalone nginx container
Static HTML with client-side GitHub API fetch for download links, replaces server-side Go template.

## feature
Proxy integration with config service
New /api/validate-key endpoint + reverse proxy forwarding /api/client-config to config container.
