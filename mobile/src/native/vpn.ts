import { NativeModules, Platform } from "react-native";

interface VpnBridge {
  connect(server: string, key: string): Promise<void>;
  disconnect(): Promise<void>;
  getStatus(): Promise<{ connected: boolean; error: string | null }>;
}

const VpnModule: VpnBridge | null =
  Platform.OS === "android" ? NativeModules.VpnBridge : null;

export async function vpnConnect(server: string, key: string): Promise<void> {
  if (!VpnModule) {
    console.warn("[vpn] Native VPN module not available on this platform");
    return;
  }
  return VpnModule.connect(server, key);
}

export async function vpnDisconnect(): Promise<void> {
  if (!VpnModule) return;
  return VpnModule.disconnect();
}

export async function vpnGetStatus(): Promise<{ connected: boolean; error: string | null }> {
  if (!VpnModule) return { connected: false, error: null };
  return VpnModule.getStatus();
}
