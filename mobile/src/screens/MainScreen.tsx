import React from "react";
import { View, Text, Pressable, StyleSheet } from "react-native";
import { Video, ResizeMode } from "expo-av";
import { LinearGradient } from "expo-linear-gradient";
import { colors } from "../theme/colors";
import { fonts } from "../theme/fonts";
import { AnimatedView } from "../components/AnimatedView";
import { StatusDot } from "../components/StatusDot";
import { SegmentedSwitch } from "../components/SegmentedSwitch";
import { useAppStore } from "../store/appStore";

function fmtSpeed(b: number): string {
  if (b < 1024) return `${Math.round(b)} B/s`;
  if (b < 1048576) return `${(b / 1024).toFixed(1)} KB/s`;
  return `${(b / 1048576).toFixed(1)} MB/s`;
}

function fmtUptime(s: number): string {
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return [h, m, sec].map((v) => String(v).padStart(2, "0")).join(":");
}

interface Props {
  activeTab: "main" | "settings";
  onTabChange: (tab: "main" | "settings") => void;
}

export function HeroZone({ activeTab, onTabChange }: Props) {
  const status = useAppStore((s) => s.status);
  const download = useAppStore((s) => s.download);
  const upload = useAppStore((s) => s.upload);
  const uptime = useAppStore((s) => s.uptime);
  const trafficMode = useAppStore((s) => s.trafficMode);
  const setTrafficMode = useAppStore((s) => s.setTrafficMode);
  const activeTransport = useAppStore((s) => s.activeTransport);
  const connect = useAppStore((s) => s.connect);
  const disconnect = useAppStore((s) => s.disconnect);

  const isConnected = status === "connected";
  const isReconnecting = status === "reconnecting";
  const isConnecting = status === "connecting";

  const modeTabAccent = isConnected ? colors.am : colors.t2;

  return (
    <View style={styles.heroContainer}>
      <Video
        source={require("../assets/earth-bg.mp4")}
        shouldPlay={isConnected}
        isLooping
        isMuted
        resizeMode={ResizeMode.COVER}
        style={styles.heroVideo}
      />
      <LinearGradient
        colors={[colors.overlayTop, colors.overlayMid, colors.overlayBot]}
        locations={[0, 0.7, 1]}
        style={StyleSheet.absoluteFillObject}
      />

      {/* Status row */}
      <View style={styles.statusRow}>
        <StatusDot isConnected={isConnected} isReconnecting={isReconnecting} />
        <View style={styles.statusText}>
          <AnimatedView kind="heavy" delay={0.25}>
            <Text style={[
              styles.statusTitle,
              {
                color: isReconnecting ? colors.am
                  : isConnected ? colors.t1
                  : colors.t3,
              },
            ]}>
              {isConnected ? "Connected"
                : isReconnecting ? "Reconnecting"
                : "Disconnected"}
            </Text>
          </AnimatedView>
          <AnimatedView kind="light" delay={0.35}>
            <View style={styles.subtitleRow}>
              {isConnected ? (
                <>
                  <Text style={styles.serverText}>
                    {useAppStore.getState().server.replace(":443", "")}
                  </Text>
                  <View style={styles.badge}>
                    <Text style={styles.badgeText}>
                      {activeTransport || "UDP"}
                    </Text>
                  </View>
                </>
              ) : isReconnecting ? (
                <Text style={styles.serverText}>Restoring connection</Text>
              ) : (
                <Text style={styles.serverText}>Ready to connect</Text>
              )}
            </View>
          </AnimatedView>
        </View>

        {/* Metrics + button */}
        <View style={styles.metricsCol}>
          {isConnected && (
            <>
              <AnimatedView kind="fade" delay={0.35} style={styles.metric}>
                <Text style={[styles.metricValue, { color: colors.gn }]}>
                  {"↓ "}{fmtSpeed(download)}
                </Text>
                <Text style={styles.metricLabel}>Down</Text>
              </AnimatedView>
              <AnimatedView kind="fade" delay={0.4} style={styles.metric}>
                <Text style={[styles.metricValue, { color: colors.bl }]}>
                  {"↑ "}{fmtSpeed(upload)}
                </Text>
                <Text style={styles.metricLabel}>Up</Text>
              </AnimatedView>
              <AnimatedView kind="fade" delay={0.45} style={styles.metric}>
                <Text style={[styles.metricValue, { color: colors.t2 }]}>
                  {fmtUptime(uptime)}
                </Text>
                <Text style={styles.metricLabel}>Uptime</Text>
              </AnimatedView>
            </>
          )}
        </View>
      </View>

      {/* Connect button */}
      <View style={styles.buttonRow}>
        <AnimatedView kind="light" delay={isConnected ? 0.5 : 0.35}>
          <Pressable
            onPress={() => {
              if (isConnected || isReconnecting) disconnect();
              else connect();
            }}
            disabled={isConnecting}
            style={({ pressed }) => [
              styles.connectBtn,
              isConnected || isReconnecting ? styles.disconnectBtn : styles.connectBtnPrimary,
              { opacity: isConnecting ? 0.5 : pressed ? 0.8 : 1 },
            ]}
          >
            <Text style={[
              styles.connectBtnText,
              { color: isConnected || isReconnecting ? colors.rd : colors.am },
            ]}>
              {isReconnecting ? "Cancel"
                : isConnected ? "Disconnect"
                : isConnecting ? "..." : "Connect"}
            </Text>
          </Pressable>
        </AnimatedView>
      </View>

      {/* Traffic mode sub-row */}
      <AnimatedView kind="light" delay={0.5} style={styles.trafficModeRow}>
        <SegmentedSwitch
          items={[
            { key: "all" as const, label: "All traffic" },
            { key: "selected" as const, label: "Selected" },
          ]}
          active={trafficMode}
          onChange={setTrafficMode}
          isConnected={isConnected}
        />
      </AnimatedView>

      {/* Tab bar */}
      <View style={styles.tabBar}>
        <Pressable
          onPress={() => onTabChange("main")}
          style={[
            styles.tab,
            activeTab === "main" && { borderBottomColor: modeTabAccent },
          ]}
        >
          <Text style={[
            styles.tabText,
            {
              color: activeTab === "main" ? modeTabAccent : colors.t3,
              fontWeight: activeTab === "main" ? "600" : "500",
            },
          ]}>
            Main
          </Text>
        </Pressable>
        <View style={{ flex: 1 }} />
        <Pressable
          onPress={() => onTabChange("settings")}
          style={[
            styles.tab,
            activeTab === "settings" && { borderBottomColor: modeTabAccent },
          ]}
        >
          <Text style={[
            styles.tabText,
            {
              color: activeTab === "settings" ? modeTabAccent : colors.t3,
              fontWeight: activeTab === "settings" ? "600" : "500",
            },
          ]}>
            {"⚙ Settings"}
          </Text>
        </Pressable>
      </View>
    </View>
  );
}

export function MainContent() {
  const trafficMode = useAppStore((s) => s.trafficMode);
  const isConnected = useAppStore((s) => s.status) === "connected";

  if (trafficMode === "all") {
    return (
      <View style={styles.mainContent}>
        <AnimatedView kind="heavy" delay={0.05}>
          <Text style={styles.allTitle}>All system traffic routed through proxy</Text>
        </AnimatedView>
        <AnimatedView kind="light" delay={0.15}>
          <Text style={styles.allDesc}>
            Every connection from this device goes through the server.{"\n"}
            Switch to Selected to choose specific apps and sites.
          </Text>
        </AnimatedView>
      </View>
    );
  }

  return (
    <View style={styles.mainContent}>
      <AnimatedView kind="fade">
        <Text style={styles.allTitle}>Selected apps and sites</Text>
        <Text style={styles.allDesc}>
          Choose which apps and browser sites go through the proxy.
        </Text>
      </AnimatedView>
    </View>
  );
}

const styles = StyleSheet.create({
  heroContainer: {
    position: "relative",
    overflow: "hidden",
    borderBottomWidth: 1,
    borderBottomColor: colors.b1,
    backgroundColor: colors.bg1,
  },
  heroVideo: {
    ...StyleSheet.absoluteFillObject,
    opacity: 0.4,
  },
  statusRow: {
    flexDirection: "row",
    alignItems: "center",
    paddingTop: 60,
    paddingHorizontal: 24,
    gap: 16,
  },
  statusText: {
    flex: 1,
    gap: 2,
  },
  statusTitle: {
    fontFamily: fonts.displayBold,
    fontSize: 22,
    letterSpacing: 0.3,
    lineHeight: 24,
  },
  subtitleRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  serverText: {
    fontFamily: fonts.body,
    fontSize: 12,
    color: colors.t3,
  },
  badge: {
    backgroundColor: colors.amb,
    borderRadius: 3,
    paddingVertical: 1,
    paddingHorizontal: 5,
  },
  badgeText: {
    fontFamily: fonts.display,
    fontSize: 9,
    fontWeight: "600",
    letterSpacing: 1,
    textTransform: "uppercase",
    color: colors.amd,
  },
  metricsCol: {
    alignItems: "flex-end",
    gap: 4,
  },
  metric: {
    alignItems: "flex-end",
    gap: 1,
  },
  metricValue: {
    fontFamily: fonts.mono,
    fontSize: 13,
    fontWeight: "600",
    fontVariant: ["tabular-nums"],
  },
  metricLabel: {
    fontFamily: fonts.display,
    fontSize: 8,
    fontWeight: "500",
    color: colors.t3,
    letterSpacing: 1,
    textTransform: "uppercase",
  },
  buttonRow: {
    paddingHorizontal: 24,
    paddingTop: 16,
    alignItems: "flex-end",
  },
  connectBtn: {
    borderRadius: 4,
    alignItems: "center",
    justifyContent: "center",
  },
  connectBtnPrimary: {
    backgroundColor: colors.amb,
    borderWidth: 1,
    borderColor: colors.amberBorder,
    paddingVertical: 8,
    paddingHorizontal: 24,
    minWidth: 100,
  },
  disconnectBtn: {
    backgroundColor: colors.rdb,
    borderWidth: 1,
    borderColor: colors.dangerBorder,
    paddingVertical: 5,
    paddingHorizontal: 14,
    minWidth: 80,
  },
  connectBtnText: {
    fontFamily: fonts.display,
    fontWeight: "600",
    letterSpacing: 0.5,
    fontSize: 13,
  },
  trafficModeRow: {
    paddingTop: 10,
    paddingBottom: 14,
    paddingHorizontal: 24,
    paddingLeft: 48,
  },
  tabBar: {
    flexDirection: "row",
    paddingHorizontal: 24,
    borderTopWidth: 1,
    borderTopColor: "rgba(56,59,67,0.5)",
  },
  tab: {
    paddingVertical: 8,
    paddingHorizontal: 16,
    borderBottomWidth: 2,
    borderBottomColor: "transparent",
    marginBottom: -1,
  },
  tabText: {
    fontFamily: fonts.display,
    fontSize: 13,
    letterSpacing: 0.3,
  },
  mainContent: {
    flex: 1,
    padding: 24,
    paddingTop: 40,
  },
  allTitle: {
    fontFamily: fonts.display,
    fontSize: 15,
    fontWeight: "600",
    color: colors.t2,
    letterSpacing: 0.3,
    marginBottom: 4,
  },
  allDesc: {
    fontFamily: fonts.body,
    fontSize: 13,
    color: colors.t3,
    lineHeight: 20,
  },
});
