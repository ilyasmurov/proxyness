import React, { useEffect, useState } from "react";
import { View, StyleSheet, StatusBar } from "react-native";
import { SafeAreaProvider, SafeAreaView } from "react-native-safe-area-context";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import {
  useFonts,
  BarlowSemiCondensed_300Light,
  BarlowSemiCondensed_400Regular,
  BarlowSemiCondensed_500Medium,
  BarlowSemiCondensed_600SemiBold,
  BarlowSemiCondensed_700Bold,
} from "@expo-google-fonts/barlow-semi-condensed";
import {
  Barlow_400Regular,
  Barlow_500Medium,
  Barlow_600SemiBold,
} from "@expo-google-fonts/barlow";
import {
  Figtree_400Regular,
  Figtree_500Medium,
  Figtree_600SemiBold,
} from "@expo-google-fonts/figtree";
import * as SplashScreen from "expo-splash-screen";

import { colors } from "./theme/colors";
import { SetupScreen } from "./screens/SetupScreen";
import { HeroZone, MainContent } from "./screens/MainScreen";
import { SettingsScreen } from "./screens/SettingsScreen";
import { ExtensionScreen } from "./screens/ExtensionScreen";
import { useAppStore } from "./store/appStore";

SplashScreen.preventAutoHideAsync();

export default function App() {
  const [fontsLoaded] = useFonts({
    BarlowSemiCondensed_300Light,
    BarlowSemiCondensed_400Regular,
    BarlowSemiCondensed_500Medium,
    BarlowSemiCondensed_600SemiBold,
    BarlowSemiCondensed_700Bold,
    Barlow_400Regular,
    Barlow_500Medium,
    Barlow_600SemiBold,
    Figtree_400Regular,
    Figtree_500Medium,
    Figtree_600SemiBold,
  });

  const key = useAppStore((s) => s.key);
  const loadKey = useAppStore((s) => s.loadKey);
  const trafficMode = useAppStore((s) => s.trafficMode);
  const [activeTab, setActiveTab] = useState<"main" | "settings">("main");
  const [ready, setReady] = useState(false);

  useEffect(() => {
    loadKey().then(() => setReady(true));
  }, []);

  useEffect(() => {
    if (fontsLoaded && ready) {
      SplashScreen.hideAsync();
    }
  }, [fontsLoaded, ready]);

  if (!fontsLoaded || !ready) return null;

  const showSetup = !key;

  return (
    <GestureHandlerRootView style={styles.root}>
      <SafeAreaProvider>
        <StatusBar barStyle="light-content" backgroundColor={colors.bg0} />
        <SafeAreaView style={styles.root} edges={["top"]}>
          {showSetup ? (
            <SetupScreen />
          ) : (
            <View style={styles.root}>
              <HeroZone activeTab={activeTab} onTabChange={setActiveTab} />
              {activeTab === "settings" ? (
                <SettingsScreen />
              ) : trafficMode === "selected" ? (
                <ExtensionScreen />
              ) : (
                <MainContent />
              )}
            </View>
          )}
        </SafeAreaView>
      </SafeAreaProvider>
    </GestureHandlerRootView>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.bg0,
  },
});
