## fix
Apps stuck in direct mode after 1.32.0 Panorama UI redesign
AppRules only mounted in the Selected tab and pushed empty proxy_only rules on first mount; switching back to All traffic never restored the open-routing rules, leaving Telegram and other apps bypassing the proxy.
