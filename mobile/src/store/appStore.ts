import { create } from "zustand";
import AsyncStorage from "@react-native-async-storage/async-storage";

const SERVER = "95.181.162.242:443";
const STORAGE_KEY = "smurov-proxy-key";

type ConnectionStatus = "disconnected" | "connecting" | "connected" | "reconnecting";
type TrafficMode = "all" | "selected";
type TransportMode = "auto" | "udp" | "tls";

interface AppState {
  key: string;
  server: string;
  status: ConnectionStatus;
  error: string | null;
  trafficMode: TrafficMode;
  transportMode: TransportMode;
  activeTransport: string;
  uptime: number;
  download: number;
  upload: number;
  version: string;

  setKey: (key: string) => void;
  setStatus: (status: ConnectionStatus) => void;
  setError: (error: string | null) => void;
  setTrafficMode: (mode: TrafficMode) => void;
  setTransportMode: (mode: TransportMode) => void;
  setActiveTransport: (transport: string) => void;
  setUptime: (uptime: number) => void;
  setStats: (download: number, upload: number) => void;
  setVersion: (version: string) => void;

  connect: () => Promise<void>;
  disconnect: () => Promise<void>;
  loadKey: () => Promise<void>;
  clearKey: () => Promise<void>;
}

export const useAppStore = create<AppState>((set, get) => ({
  key: "",
  server: SERVER,
  status: "disconnected",
  error: null,
  trafficMode: "all",
  transportMode: "auto",
  activeTransport: "",
  uptime: 0,
  download: 0,
  upload: 0,
  version: "1.0.0",

  setKey: (key) => {
    set({ key });
    AsyncStorage.setItem(STORAGE_KEY, key);
  },
  setStatus: (status) => set({ status }),
  setError: (error) => set({ error }),
  setTrafficMode: (mode) => set({ trafficMode: mode }),
  setTransportMode: (mode) => set({ transportMode: mode }),
  setActiveTransport: (transport) => set({ activeTransport: transport }),
  setUptime: (uptime) => set({ uptime }),
  setStats: (download, upload) => set({ download, upload }),
  setVersion: (version) => set({ version }),

  connect: async () => {
    const { key, status } = get();
    if (!key || status === "connecting") return;
    set({ status: "connecting", error: null });
    try {
      // In a real build, NativeModules.VpnBridge.connect(SERVER, key) goes here.
      // For the scaffold, simulate a brief delay then succeed.
      await new Promise((r) => setTimeout(r, 800));
      set({ status: "connected", uptime: 0 });
    } catch (e: any) {
      set({ status: "disconnected", error: e?.message || "Connection failed" });
    }
  },

  disconnect: async () => {
    set({ status: "disconnected", error: null, uptime: 0, download: 0, upload: 0 });
  },

  loadKey: async () => {
    const stored = await AsyncStorage.getItem(STORAGE_KEY);
    if (stored) set({ key: stored });
  },

  clearKey: async () => {
    await AsyncStorage.removeItem(STORAGE_KEY);
    set({ key: "", status: "disconnected", error: null });
  },
}));
