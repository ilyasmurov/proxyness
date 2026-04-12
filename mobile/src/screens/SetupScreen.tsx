import React, { useState, useRef } from "react";
import { View, Text, TextInput, StyleSheet, Dimensions, Keyboard } from "react-native";
import { Video, ResizeMode } from "expo-av";
import { LinearGradient } from "expo-linear-gradient";
import { colors } from "../theme/colors";
import { fonts } from "../theme/fonts";
import { AnimatedView } from "../components/AnimatedView";
import { useAppStore } from "../store/appStore";

const { width: SCREEN_W, height: SCREEN_H } = Dimensions.get("window");

export function SetupScreen() {
  const [inputKey, setInputKey] = useState("");
  const [loading, setLoading] = useState(false);
  const inputRef = useRef<TextInput>(null);
  const setKey = useAppStore((s) => s.setKey);
  const connect = useAppStore((s) => s.connect);

  const handleSubmit = async () => {
    const trimmed = inputKey.trim();
    if (!trimmed) return;
    Keyboard.dismiss();
    setKey(trimmed);
    setLoading(true);
    await connect();
    setLoading(false);
  };

  return (
    <View style={styles.root}>
      <Video
        source={require("../assets/earth-bg.mp4")}
        shouldPlay
        isLooping
        isMuted
        resizeMode={ResizeMode.COVER}
        style={styles.video}
      />
      <LinearGradient
        colors={["rgba(27,30,37,0.92)", "rgba(27,30,37,0.3)"]}
        start={{ x: 0, y: 0.5 }}
        end={{ x: 1, y: 0.5 }}
        style={StyleSheet.absoluteFillObject}
      />

      <View style={styles.content}>
        <AnimatedView kind="heavy" delay={0.15}>
          <Text style={styles.title}>{"Smurov\nProxy"}</Text>
        </AnimatedView>

        <AnimatedView kind="med" delay={0.3}>
          <Text style={styles.subtitle}>
            {"Secure system-level proxy\nfor apps and browsers"}
          </Text>
        </AnimatedView>

        <View style={styles.form}>
          <AnimatedView kind="light" delay={0.45}>
            <Text style={styles.fieldLabel}>Access Key</Text>
          </AnimatedView>

          <AnimatedView kind="light" delay={0.5}>
            <TextInput
              ref={inputRef}
              style={styles.input}
              secureTextEntry
              value={inputKey}
              onChangeText={setInputKey}
              placeholder="Paste your access key"
              placeholderTextColor={colors.t3}
              autoCapitalize="none"
              autoCorrect={false}
              returnKeyType="go"
              onSubmitEditing={handleSubmit}
            />
          </AnimatedView>

          <AnimatedView kind="fade" delay={0.6}>
            <Text style={styles.hint}>
              {loading ? "Connecting..." : "Paste the key — connection starts automatically"}
            </Text>
          </AnimatedView>
        </View>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.bg0,
  },
  video: {
    ...StyleSheet.absoluteFillObject,
    width: SCREEN_W,
    height: SCREEN_H,
  },
  content: {
    flex: 1,
    justifyContent: "center",
    paddingHorizontal: 32,
    paddingBottom: 60,
  },
  title: {
    fontFamily: fonts.displayLight,
    fontSize: 48,
    color: colors.t1,
    letterSpacing: 3,
    textTransform: "uppercase",
    lineHeight: 52,
    marginBottom: 4,
  },
  subtitle: {
    fontFamily: fonts.display,
    fontSize: 14,
    color: colors.amd,
    letterSpacing: 4,
    textTransform: "uppercase",
    marginBottom: 48,
  },
  form: {
    maxWidth: 320,
  },
  fieldLabel: {
    fontFamily: fonts.display,
    fontSize: 10,
    fontWeight: "600",
    color: colors.t3,
    letterSpacing: 1.5,
    textTransform: "uppercase",
    marginBottom: 8,
  },
  input: {
    width: "100%",
    paddingVertical: 10,
    paddingHorizontal: 14,
    backgroundColor: "rgba(35,38,46,0.7)",
    borderWidth: 1,
    borderColor: colors.b1,
    borderRadius: 5,
    color: colors.t1,
    fontFamily: fonts.body,
    fontSize: 14,
  },
  hint: {
    fontFamily: fonts.body,
    fontSize: 11,
    color: colors.t3,
    marginTop: 10,
  },
});
