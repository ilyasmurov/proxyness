import React from "react";
import { View, Text, Pressable, StyleSheet } from "react-native";
import { colors } from "../theme/colors";
import { fonts } from "../theme/fonts";

interface Props<T extends string> {
  items: { key: T; label: string }[];
  active: T;
  onChange: (key: T) => void;
  isConnected: boolean;
}

export function SegmentedSwitch<T extends string>({ items, active, onChange, isConnected }: Props<T>) {
  const activeColor = isConnected ? colors.am : colors.t2;
  const activeBg = isConnected ? colors.amb : colors.bg2;

  return (
    <View style={styles.container}>
      {items.map(({ key, label }) => {
        const isActive = active === key;
        return (
          <Pressable
            key={key}
            onPress={() => onChange(key)}
            style={[
              styles.item,
              isActive && { backgroundColor: activeBg },
            ]}
          >
            <Text
              style={[
                styles.label,
                {
                  color: isActive ? activeColor : colors.t3,
                  fontWeight: isActive ? "600" : "500",
                },
              ]}
            >
              {label}
            </Text>
          </Pressable>
        );
      })}
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flexDirection: "row",
    padding: 3,
    borderRadius: 6,
    backgroundColor: "rgba(35,38,46,0.6)",
    borderWidth: 1,
    borderColor: colors.b1,
    alignSelf: "flex-start",
  },
  item: {
    paddingVertical: 4,
    paddingHorizontal: 12,
    borderRadius: 4,
  },
  label: {
    fontFamily: fonts.display,
    fontSize: 11,
    letterSpacing: 0.3,
  },
});
