import React, { useEffect } from "react";
import { StyleSheet } from "react-native";
import Animated, {
  useSharedValue,
  useAnimatedStyle,
  withRepeat,
  withTiming,
  withDelay,
  Easing,
} from "react-native-reanimated";
import { colors } from "../theme/colors";
import { EASE } from "../theme/animations";

interface Props {
  isConnected: boolean;
  isReconnecting: boolean;
}

export function StatusDot({ isConnected, isReconnecting }: Props) {
  const rotation = useSharedValue(0);
  const dotScale = useSharedValue(0);
  const dotOpacity = useSharedValue(0);

  useEffect(() => {
    if (isReconnecting) {
      rotation.value = withRepeat(
        withTiming(360, { duration: 800, easing: Easing.linear }),
        -1,
        false,
      );
    } else {
      rotation.value = 0;
      dotScale.value = withDelay(200, withTiming(1, { duration: 300, easing: EASE }));
      dotOpacity.value = withDelay(200, withTiming(1, { duration: 300, easing: EASE }));
    }
  }, [isReconnecting]);

  const spinStyle = useAnimatedStyle(() => ({
    transform: [{ rotate: `${rotation.value}deg` }],
  }));

  const dotStyle = useAnimatedStyle(() => ({
    opacity: dotOpacity.value,
    transform: [{ scale: dotScale.value }],
  }));

  if (isReconnecting) {
    return (
      <Animated.View style={[styles.spinner, spinStyle]}>
        <Animated.View style={styles.spinnerInner} />
      </Animated.View>
    );
  }

  return (
    <Animated.View
      style={[
        styles.dot,
        { backgroundColor: isConnected ? colors.am : colors.t3 },
        dotStyle,
      ]}
    />
  );
}

const styles = StyleSheet.create({
  dot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  spinner: {
    width: 14,
    height: 14,
    borderRadius: 7,
    borderWidth: 2,
    borderColor: colors.am,
    borderTopColor: "transparent",
  },
  spinnerInner: {
    flex: 1,
  },
});
