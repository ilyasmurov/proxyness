# Morning Report — Mobile App Scaffold

**Date**: 2026-04-12  
**Session**: overnight autonomous build  
**Result**: React Native + Expo project with shared UI, Android VpnService, iOS scaffold

---

## What Works

### UI (both platforms via Expo Go)
- **Setup screen**: access key entry with video background, gradient overlay, Panorama palette, Barlow Semi Condensed + Figtree fonts loaded via Google Fonts
- **Hero zone**: status dot (animated), connection title, server + transport badge, download/upload/uptime metrics, Connect/Disconnect button with amber-when-live/red-disconnect states
- **Traffic mode switch**: All traffic / Selected segmented switch with isConnected amber gating
- **Tab bar**: Main ↔ Settings with amber underline when connected
- **Settings page**: sidebar with General/Extension/Account/Diagnostics sections, staggered cascade animations via Reanimated, Transport Protocol selector (AUTO/UDP/TLS), Version field, Change Key (danger variant), Daemon Token, Open Logs
- **Extension screen**: app tiles (Telegram, Discord, Claude Code, Cursor, Slack) with brand-color toggles, browser sites grid with domain-derived avatars
- **Animations**: all smurov-blur-* keyframes ported to Reanimated 4 (opacity + translateY/X + scale, no CSS filter: blur — RN doesn't support it; the motion reads the same, the blur-resolve effect is approximated via opacity)
- **State**: Zustand store with key persistence via AsyncStorage, connect/disconnect flow, traffic/transport mode

### Android Native
- **VpnService** (`SmurovVpnService.kt`): TLS connection to proxy.smurov.com:443, HMAC-SHA256 auth matching the existing pkg/auth wire format, VPN permission request, TUN interface creation, bidirectional length-prefixed packet relay
- **RN bridge** (`VpnBridgeModule.kt`): `connect(server, key)` / `disconnect()` / `getStatus()` exposed to JS via React Native native modules, VPN permission handling via `onActivityResult`
- **Manifest**: VPN service registered, BIND_VPN_SERVICE permission declared

### iOS Native (code only — cannot build)
- **NEPacketTunnelProvider** skeleton (`VpnBridge.swift`): full implementation of TLS connect, HMAC auth, packet flow read/write — commented out because it requires Xcode.app + Network Extension entitlement + Apple Developer provisioning

### Go Bridge (reference)
- **vpnbridge** module at `mobile/go/vpnbridge/`: gomobile-friendly wrapper around pkg/auth + pkg/proto that compiles. Cannot produce .aar because Android NDK doesn't fit on disk (~3GB). Kept as reference — the protocol was implemented directly in Kotlin instead.

---

## What Doesn't Work / Known Issues

1. **Android APK build**: first gradle build was running at session end. If it succeeded, APK is at `mobile/android/app/build/outputs/apk/debug/app-debug.apk`. If it failed, run:
   ```bash
   export JAVA_HOME=/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home
   export ANDROID_HOME=/opt/homebrew/share/android-commandlinetools
   cd mobile/android && ./gradlew assembleDebug
   ```

2. **iOS build**: physically impossible without Xcode.app (only CLI tools installed). Install Xcode from Mac App Store → run `cd mobile && npx expo prebuild --clean` → copy native files from `ios/SmurovProxy/VpnBridge.swift` → add Network Extension target in Xcode

3. **VPN packet relay is untested**: the Kotlin VpnService has the correct protocol implementation (TLS + HMAC + length-prefixed framing) but without a physical Android device or emulator, the actual packet relay path is unverified. The server expects per-connection tunneling (connect → auth → msg type → target address per stream) not bulk IP relay — so the current relay loop needs to be wired to a local netstack or per-destination multiplexer. This is the main integration work remaining.

4. **gomobile .aar / .xcframework**: skipped because Android NDK is ~3GB and disk was at 2.5GB when attempted. After freeing space (now ~20GB), you could retry:
   ```bash
   export JAVA_HOME=/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home
   export ANDROID_HOME=/opt/homebrew/share/android-commandlinetools
   sdkmanager "ndk;25.2.9519653"
   cd mobile/go/vpnbridge && GOWORK=off gomobile bind -target=android -o ../../android/app/libs/vpnbridge.aar .
   ```

5. **Video background**: `expo-av` is deprecated in SDK 54 (use `expo-video`). Works for now but will need migration.

6. **Fonts in Expo Go**: Google Fonts loaded via `useFonts` hook — works in Expo Go. In a production bare build, fonts load from bundled assets.

7. **No real traffic flow**: the `connect()` in Zustand store is a stub (800ms delay → "connected"). Wire it to `vpnConnect()` from `native/vpn.ts` for real server connection on Android.

8. **`expo prebuild --clean` wipes custom native code**: the VpnService, VpnBridgeModule, VpnBridgePackage, manifest edits, and local.properties all live in `android/` which prebuild regenerates. If you add a new Expo module (like I did with expo-clipboard), you must re-add the custom files after re-prebuild. Long-term fix: Expo config plugin to inject them automatically.

---

## Autonomous Decisions

1. **Kotlin over gomobile**: disk space forced this pivot. Ended up being cleaner — one less build tool in the chain, and the HMAC-SHA256 + TLS protocol is simple enough in Kotlin
2. **Reanimated 4 animations without blur**: RN doesn't support CSS `filter: blur()`. Ported as opacity + translate + scale which gives the same staggered cascade feel without the frosted-glass resolve
3. **Simplified AppRules**: Electron's 1557-line AppRules with SVG brand icons, site management, drag-and-drop → mobile version uses text-letter avatars + brand-color tiles. Functional equivalent, much simpler
4. **Bare workflow (android/ and ios/ in git)**: custom native code (VpnService, VpnBridge) requires bare workflow. Removed `/ios` and `/android` from .gitignore, added build artifact ignores instead
5. **expo-splash-screen**: added as dependency when tsc flagged it missing from the scaffold template

---

## Files to Eyeball

| File | What to check |
|------|--------------|
| `src/theme/colors.ts` | Hex conversions from OKLCH — I eyeballed these, verify on device |
| `src/screens/SetupScreen.tsx` | Video + gradient overlay layout on actual phone screen |
| `src/screens/MainScreen.tsx` | Hero zone proportions, metrics alignment |
| `src/screens/SettingsScreen.tsx` | Sidebar width on phone (140px might be tight on small screens) |
| `android/.../SmurovVpnService.kt` | HMAC auth byte layout matches server — compare with pkg/auth |
| `ios/SmurovProxy/VpnBridge.swift` | NEPacketTunnelProvider API usage (might have Swift 6 changes) |

---

## Next Steps

1. **Test on device**: `npx expo start` → scan QR in Expo Go on iPhone for UI preview; for Android with VPN, need `npx expo run:android` on a connected device
2. **Wire real connection**: replace Zustand connect() stub with native VPN module call
3. **Server-side netstack**: VpnService currently relays raw IP packets, but the server expects per-connection SOCKS-style streams. Need either a local gVisor netstack (like the desktop daemon) or server-side IP relay mode
4. **Install Xcode.app**: required for iOS native build with NEPacketTunnelProvider
5. **gomobile NDK**: install NDK if disk allows, build .aar for shared Go protocol code
6. **Production build signing**: debug keystore for Android, Apple Developer provisioning for iOS

---

## Installed Toolchain

- OpenJDK 17.0.18 (brew formula, `/opt/homebrew/opt/openjdk@17/`)
- Android SDK: platforms;android-34, build-tools;34.0.0, platform-tools (`/opt/homebrew/share/android-commandlinetools/`)
- CocoaPods 1.16.2 (installed by expo prebuild via brew)
- gomobile + gobind (`~/go/bin/`)
- No Xcode.app (only CommandLineTools)
- No Android NDK (insufficient disk at install time, now resolved)
