import React, { useState } from "react";
import { View, Text, Pressable, ScrollView, Switch, StyleSheet } from "react-native";
import { colors } from "../theme/colors";
import { fonts } from "../theme/fonts";
import { AnimatedView } from "../components/AnimatedView";
import { useAppStore } from "../store/appStore";

interface KnownApp {
  id: string;
  name: string;
  color: string;
  letter: string;
}

const KNOWN_APPS: KnownApp[] = [
  { id: "telegram", name: "Telegram", color: "#27A7E7", letter: "T" },
  { id: "discord", name: "Discord", color: "#5865F2", letter: "D" },
  { id: "claude", name: "Claude Code", color: "#D97757", letter: "C" },
  { id: "cursor", name: "Cursor", color: "#00D1FF", letter: "Cu" },
  { id: "slack", name: "Slack", color: "#E01E5A", letter: "S" },
];

const KNOWN_SITES = [
  { domain: "youtube.com", color: "#FF0000", letter: "Y" },
  { domain: "instagram.com", color: "#E4405F", letter: "I" },
  { domain: "twitter.com", color: "#999999", letter: "X" },
  { domain: "facebook.com", color: "#0866FF", letter: "F" },
  { domain: "linkedin.com", color: "#0A66C2", letter: "L" },
  { domain: "claude.ai", color: "#D97757", letter: "C" },
];

function AppTile({ app, enabled, onToggle }: {
  app: KnownApp;
  enabled: boolean;
  onToggle: () => void;
}) {
  return (
    <Pressable onPress={onToggle} style={styles.tile}>
      <View style={[styles.tileIcon, { backgroundColor: app.color + "20" }]}>
        <Text style={[styles.tileIconLetter, { color: app.color }]}>
          {app.letter}
        </Text>
      </View>
      <Text style={styles.tileName} numberOfLines={1}>{app.name}</Text>
      <Switch
        value={enabled}
        onValueChange={onToggle}
        trackColor={{ false: colors.bg2, true: app.color + "60" }}
        thumbColor={enabled ? app.color : colors.t3}
        style={styles.tileSwitch}
      />
    </Pressable>
  );
}

function SiteTile({ domain, color, letter, enabled, onToggle }: {
  domain: string;
  color: string;
  letter: string;
  enabled: boolean;
  onToggle: () => void;
}) {
  const label = domain.replace(/^www\./, "").split(".")[0];
  const displayLabel = label ? label.charAt(0).toUpperCase() + label.slice(1) : domain;

  return (
    <Pressable onPress={onToggle} style={styles.siteTile}>
      <View style={[styles.siteIcon, { backgroundColor: color + "20" }]}>
        <Text style={[styles.siteIconLetter, { color }]}>{letter}</Text>
      </View>
      <Text style={styles.siteName} numberOfLines={1}>{displayLabel}</Text>
      {enabled && <View style={[styles.activeDot, { backgroundColor: color }]} />}
    </Pressable>
  );
}

export function ExtensionScreen() {
  const isConnected = useAppStore((s) => s.status) === "connected";
  const [enabledApps, setEnabledApps] = useState<Set<string>>(
    new Set(KNOWN_APPS.map((a) => a.id)),
  );
  const [enabledSites, setEnabledSites] = useState<Set<string>>(
    new Set(KNOWN_SITES.map((s) => s.domain)),
  );

  const toggleApp = (id: string) => {
    setEnabledApps((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleSite = (domain: string) => {
    setEnabledSites((prev) => {
      const next = new Set(prev);
      if (next.has(domain)) next.delete(domain);
      else next.add(domain);
      return next;
    });
  };

  return (
    <ScrollView style={styles.root} contentContainerStyle={styles.content}>
      <AnimatedView kind="heavy" delay={0.05}>
        <Text style={styles.sectionTitle}>Apps</Text>
      </AnimatedView>
      <AnimatedView kind="light" delay={0.1}>
        <Text style={styles.sectionDesc}>
          Choose which apps route through the proxy via TUN.
        </Text>
      </AnimatedView>

      <View style={styles.tileGrid}>
        {KNOWN_APPS.map((app, i) => (
          <AnimatedView key={app.id} kind="row" delay={0.15 + i * 0.05}>
            <AppTile
              app={app}
              enabled={enabledApps.has(app.id)}
              onToggle={() => toggleApp(app.id)}
            />
          </AnimatedView>
        ))}
      </View>

      <View style={styles.divider} />

      <AnimatedView kind="heavy" delay={0.4}>
        <Text style={styles.sectionTitle}>Browser Sites</Text>
      </AnimatedView>
      <AnimatedView kind="light" delay={0.45}>
        <Text style={styles.sectionDesc}>
          Sites that route through SOCKS5 proxy in the browser.
        </Text>
      </AnimatedView>

      <View style={styles.siteGrid}>
        {KNOWN_SITES.map((site, i) => (
          <AnimatedView key={site.domain} kind="fade" delay={0.5 + i * 0.04}>
            <SiteTile
              domain={site.domain}
              color={site.color}
              letter={site.letter}
              enabled={enabledSites.has(site.domain)}
              onToggle={() => toggleSite(site.domain)}
            />
          </AnimatedView>
        ))}
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
  },
  content: {
    padding: 20,
    paddingBottom: 40,
  },
  sectionTitle: {
    fontFamily: fonts.display,
    fontSize: 15,
    fontWeight: "600",
    color: colors.t2,
    letterSpacing: 0.3,
    marginBottom: 4,
  },
  sectionDesc: {
    fontFamily: fonts.body,
    fontSize: 12,
    color: colors.t3,
    marginBottom: 16,
    lineHeight: 18,
  },
  tileGrid: {
    gap: 8,
  },
  tile: {
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: colors.bg1,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: colors.b1,
    paddingVertical: 10,
    paddingHorizontal: 12,
    gap: 12,
  },
  tileIcon: {
    width: 36,
    height: 36,
    borderRadius: 8,
    alignItems: "center",
    justifyContent: "center",
  },
  tileIconLetter: {
    fontFamily: fonts.displayBold,
    fontSize: 14,
    fontWeight: "700",
  },
  tileName: {
    fontFamily: fonts.body,
    fontSize: 13,
    color: colors.t1,
    flex: 1,
  },
  tileSwitch: {
    transform: [{ scaleX: 0.8 }, { scaleY: 0.8 }],
  },
  divider: {
    height: 1,
    backgroundColor: colors.b1,
    marginVertical: 20,
  },
  siteGrid: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 10,
  },
  siteTile: {
    width: 80,
    alignItems: "center",
    gap: 4,
    paddingVertical: 10,
  },
  siteIcon: {
    width: 44,
    height: 44,
    borderRadius: 22,
    alignItems: "center",
    justifyContent: "center",
  },
  siteIconLetter: {
    fontFamily: fonts.displayBold,
    fontSize: 16,
    fontWeight: "700",
  },
  siteName: {
    fontFamily: fonts.body,
    fontSize: 10,
    color: colors.t3,
  },
  activeDot: {
    width: 4,
    height: 4,
    borderRadius: 2,
  },
});
