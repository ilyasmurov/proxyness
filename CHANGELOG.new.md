## improvement
Browser sites picker redesigned as a grid of brand tiles
Selected-apps mode now shows a 4-column grid of site tiles instead of the old collapsible list. The first tile is a wide "All browsers" card that toggles the wildcard proxy; when it's on, individual tiles dim to show they're overridden. Clicking any tile while All browsers is on switches to selected-sites mode and enables that tile. Tile colors come from the site's brand color, monochrome when disabled.

## feature
Add site modal with live favicon preview
Click "+ Add site" in the Browser sites section to open a modal. Type a domain and the preview tile updates in real time: Google's S2 favicons API is used for the icon, with a letter-avatar fallback (gradient background, hashed hue per domain) when no favicon is available. Esc or clicking the backdrop closes the modal.
