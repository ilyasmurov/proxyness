import React, { useState, useCallback } from "react";
import { View, Text, Pressable, ScrollView, StyleSheet, Alert } from "react-native";
import { colors } from "../theme/colors";
import { fonts } from "../theme/fonts";
import { AnimatedView } from "../components/AnimatedView";
import { SegmentedSwitch } from "../components/SegmentedSwitch";
import { Button } from "../components/Button";
import { useAppStore } from "../store/appStore";

type Section = "general" | "extension" | "account" | "diagnostics";
const NAV_SECTIONS: Section[] = ["general", "extension", "account", "diagnostics"];

export function SettingsScreen() {
  const [section, setSection] = useState<Section>("general");
  const [copied, setCopied] = useState(false);
  const isConnected = useAppStore((s) => s.status) === "connected";
  const transportMode = useAppStore((s) => s.transportMode);
  const setTransportMode = useAppStore((s) => s.setTransportMode);
  const version = useAppStore((s) => s.version);
  const clearKey = useAppStore((s) => s.clearKey);

  const copyToken = useCallback(() => {
    Alert.alert("Daemon Token", "(daemon not running)", [{ text: "OK" }]);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }, []);

  const navItem = (id: Section, label: string) => {
    const active = section === id;
    return (
      <Pressable
        key={id}
        onPress={() => setSection(id)}
        accessibilityRole="tab"
        accessibilityState={{ selected: active }}
        style={[
          styles.navItem,
          active && { backgroundColor: colors.bg2 },
        ]}
      >
        <Text style={[
          styles.navLabel,
          { color: active ? colors.t1 : colors.t3 },
        ]}>
          {label}
        </Text>
      </Pressable>
    );
  };

  const fieldLabel = (text: string, delay = 0) => (
    <AnimatedView kind="fade" delay={delay}>
      <Text style={styles.fieldLabel}>{text}</Text>
    </AnimatedView>
  );

  const divider = (delay: number) => (
    <AnimatedView kind="fade" delay={delay}>
      <View style={styles.divider} />
    </AnimatedView>
  );

  return (
    <View style={styles.root}>
      {/* Sidebar */}
      <View style={styles.sidebar}>
        <AnimatedView kind="row" delay={0.05}>{navItem("general", "General")}</AnimatedView>
        <AnimatedView kind="row" delay={0.1}>{navItem("extension", "Extension")}</AnimatedView>
        <AnimatedView kind="row" delay={0.15}>{navItem("account", "Account")}</AnimatedView>
        <AnimatedView kind="row" delay={0.2}>{navItem("diagnostics", "Diagnostics")}</AnimatedView>
      </View>

      {/* Panel */}
      <ScrollView key={section} style={styles.panel} contentContainerStyle={styles.panelContent}>
        {section === "general" && (
          <>
            <AnimatedView kind="heavy" delay={0.05}>
              <Text style={styles.sectionTitle}>General</Text>
            </AnimatedView>
            <AnimatedView kind="light" delay={0.1}>
              <Text style={styles.sectionDesc}>App version and connection settings.</Text>
            </AnimatedView>

            {fieldLabel("Version", 0.15)}
            <AnimatedView kind="row" delay={0.2}>
              <View style={styles.fieldRow}>
                <Text style={styles.fieldValue}>{version || "—"}</Text>
                <Button label="Check for updates" onPress={() => {}} />
              </View>
            </AnimatedView>

            {divider(0.25)}

            {fieldLabel("Transport Protocol", 0.3)}
            <AnimatedView kind="light" delay={0.35}>
              <SegmentedSwitch
                items={[
                  { key: "auto" as const, label: "AUTO" },
                  { key: "udp" as const, label: "UDP" },
                  { key: "tls" as const, label: "TLS" },
                ]}
                active={transportMode}
                onChange={(m) => setTransportMode(m as any)}
                isConnected={isConnected}
              />
            </AnimatedView>
          </>
        )}

        {section === "extension" && (
          <>
            <AnimatedView kind="heavy" delay={0.05}>
              <Text style={styles.sectionTitle}>Browser Extension</Text>
            </AnimatedView>
            <AnimatedView kind="light" delay={0.1}>
              <Text style={styles.sectionDesc}>
                Use this token to connect the browser extension to the local daemon.
              </Text>
            </AnimatedView>

            {fieldLabel("Daemon Token", 0.15)}
            <AnimatedView kind="row" delay={0.2}>
              <View style={styles.fieldRow}>
                <View style={styles.tokenBox}>
                  <Text style={styles.tokenText} numberOfLines={1}>
                    (daemon not running)
                  </Text>
                </View>
                <Button
                  label={copied ? "Copied" : "Copy"}
                  onPress={copyToken}
                  size="sm"
                />
              </View>
            </AnimatedView>
          </>
        )}

        {section === "account" && (
          <>
            <AnimatedView kind="heavy" delay={0.05}>
              <Text style={styles.sectionTitle}>Account</Text>
            </AnimatedView>
            <AnimatedView kind="light" delay={0.1}>
              <Text style={styles.sectionDesc}>Manage your device connection.</Text>
            </AnimatedView>

            {fieldLabel("Access Key", 0.15)}
            <AnimatedView kind="row" delay={0.2}>
              <View style={styles.fieldRow}>
                <Text style={[styles.fieldValue, { flex: 1 }]}>
                  Disconnect and enter a different access key.
                </Text>
                <Button label="Change Key" onPress={clearKey} variant="danger" />
              </View>
            </AnimatedView>
          </>
        )}

        {section === "diagnostics" && (
          <>
            <AnimatedView kind="heavy" delay={0.05}>
              <Text style={styles.sectionTitle}>Diagnostics</Text>
            </AnimatedView>
            <AnimatedView kind="light" delay={0.1}>
              <Text style={styles.sectionDesc}>View daemon and helper output.</Text>
            </AnimatedView>

            {fieldLabel("Logs", 0.15)}
            <AnimatedView kind="row" delay={0.2}>
              <View style={styles.fieldRow}>
                <Text style={[styles.fieldValue, { flex: 1 }]}>
                  Open the log viewer window.
                </Text>
                <Button label="Open Logs" onPress={() => {}} />
              </View>
            </AnimatedView>
          </>
        )}
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    flexDirection: "row",
  },
  sidebar: {
    width: 140,
    borderRightWidth: 1,
    borderRightColor: colors.b1,
    paddingVertical: 16,
    gap: 1,
  },
  navItem: {
    paddingVertical: 7,
    paddingHorizontal: 16,
  },
  navLabel: {
    fontFamily: fonts.body,
    fontSize: 12,
    fontWeight: "500",
  },
  panel: {
    flex: 1,
  },
  panelContent: {
    padding: 20,
    paddingRight: 24,
  },
  sectionTitle: {
    fontFamily: fonts.displayBold,
    fontSize: 16,
    color: colors.t1,
    letterSpacing: 0.3,
    marginBottom: 4,
  },
  sectionDesc: {
    fontFamily: fonts.body,
    fontSize: 12,
    color: colors.t3,
    marginBottom: 20,
    lineHeight: 18,
  },
  fieldLabel: {
    fontFamily: fonts.display,
    fontSize: 10,
    fontWeight: "600",
    color: colors.t3,
    letterSpacing: 1.5,
    textTransform: "uppercase",
    marginBottom: 6,
  },
  fieldRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
  },
  fieldValue: {
    fontFamily: fonts.mono,
    fontSize: 12,
    color: colors.t2,
  },
  divider: {
    height: 1,
    backgroundColor: colors.b1,
    marginVertical: 16,
  },
  tokenBox: {
    flex: 1,
    paddingVertical: 6,
    paddingHorizontal: 10,
    backgroundColor: colors.bg2,
    borderWidth: 1,
    borderColor: colors.b1,
    borderRadius: 5,
  },
  tokenText: {
    fontFamily: fonts.mono,
    fontSize: 11,
    color: colors.t2,
  },
});
