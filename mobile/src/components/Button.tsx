import React from "react";
import { Text, Pressable, StyleSheet, ViewStyle, TextStyle } from "react-native";
import { colors } from "../theme/colors";
import { fonts } from "../theme/fonts";

interface Props {
  label: string;
  onPress: () => void;
  variant?: "primary" | "secondary" | "danger";
  size?: "sm" | "md" | "lg";
  disabled?: boolean;
  style?: ViewStyle;
}

export function Button({ label, onPress, variant = "secondary", size = "md", disabled, style }: Props) {
  const bg = variant === "primary" ? colors.amb
    : variant === "danger" ? colors.rdb
    : colors.bg2;
  const borderColor = variant === "primary" ? colors.amberBorder
    : variant === "danger" ? colors.dangerBorder
    : colors.b1;
  const fg = variant === "primary" ? colors.am
    : variant === "danger" ? colors.rd
    : colors.t2;

  const padding = size === "lg"
    ? { paddingVertical: 8, paddingHorizontal: 24 }
    : size === "sm"
    ? { paddingVertical: 4, paddingHorizontal: 10 }
    : { paddingVertical: 5, paddingHorizontal: 14 };

  const fontSize = size === "lg" ? 13 : size === "sm" ? 10 : 12;

  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      style={({ pressed }) => [
        styles.base,
        { backgroundColor: bg, borderColor, opacity: disabled ? 0.5 : pressed ? 0.8 : 1 },
        padding,
        style,
      ]}
    >
      <Text style={[styles.label, { color: fg, fontSize }]}>
        {label}
      </Text>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  base: {
    borderRadius: 5,
    borderWidth: 1,
    alignItems: "center",
    justifyContent: "center",
  },
  label: {
    fontFamily: fonts.display,
    fontWeight: "600",
    letterSpacing: 0.3,
  },
});
