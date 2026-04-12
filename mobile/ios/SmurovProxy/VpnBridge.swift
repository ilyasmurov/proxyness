// NEPacketTunnelProvider scaffold for iOS VPN.
// Cannot compile or test without full Xcode.app — this is the
// code skeleton that's ready to build once Xcode is installed.
//
// The protocol matches SmurovVpnService.kt on Android:
// 1. TLS dial to proxy.smurov.com:443
// 2. Send MsgTypeTCP (0x01)
// 3. Send 41-byte auth: version(1) + timestamp(8) + HMAC-SHA256(32)
// 4. Read 1-byte result (0x01 = OK)
// 5. Establish TUN, relay length-prefixed IP packets
//
// See MORNING_REPORT.md for full setup instructions.
