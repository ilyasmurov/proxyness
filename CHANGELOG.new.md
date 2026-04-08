## fix
Auto-connect on cold start
Client now re-establishes the SOCKS5 tunnel automatically on launch when a device key is stored, instead of leaving the browser proxy off until you click Connect.

## fix
Daemon CORS for dev builds
Added CORS headers on public daemon endpoints so `make dev` renderer (http://localhost:5174) can actually reach the API on 127.0.0.1:9090.

## feature
Finish scanning button in the extension
The discovery panel now has a "Finish scanning" button to dismiss the scanner on sites where you're done adding domains.

## feature
Search on admin Sites page
Admin panel Sites table now has a search box that filters by label, primary domain, or slug.
