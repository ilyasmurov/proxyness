import React, { useEffect } from "react";
import { ViewStyle } from "react-native";
import Animated, {
  useSharedValue,
  useAnimatedStyle,
  withDelay,
  withTiming,
} from "react-native-reanimated";
import { EASE, type AnimKind } from "../theme/animations";

interface Props {
  kind: AnimKind;
  delay?: number;
  children: React.ReactNode;
  style?: ViewStyle;
}

const DUR: Record<AnimKind, number> = {
  heavy: 500,
  med: 450,
  light: 400,
  fade: 400,
  row: 300,
  dot: 300,
  badge: 300,
};

const TRANSLATE_Y: Partial<Record<AnimKind, number>> = {
  heavy: 8,
  med: 5,
  light: 3,
};

const TRANSLATE_X: Partial<Record<AnimKind, number>> = {
  row: -6,
};

const START_SCALE: Partial<Record<AnimKind, number>> = {
  dot: 0,
  badge: 0.8,
};

export function AnimatedView({ kind, delay = 0, children, style }: Props) {
  const opacity = useSharedValue(0);
  const translateY = useSharedValue(TRANSLATE_Y[kind] ?? 0);
  const translateX = useSharedValue(TRANSLATE_X[kind] ?? 0);
  const scale = useSharedValue(START_SCALE[kind] ?? 1);

  useEffect(() => {
    const dur = DUR[kind];
    const cfg = { duration: dur, easing: EASE };
    const d = delay * 1000;
    opacity.value = withDelay(d, withTiming(1, cfg));
    translateY.value = withDelay(d, withTiming(0, cfg));
    translateX.value = withDelay(d, withTiming(0, cfg));
    scale.value = withDelay(d, withTiming(1, cfg));
  }, [kind, delay]);

  const animStyle = useAnimatedStyle(() => ({
    opacity: opacity.value,
    transform: [
      { translateY: translateY.value },
      { translateX: translateX.value },
      { scale: scale.value },
    ],
  }));

  return (
    <Animated.View style={[animStyle, style]}>
      {children}
    </Animated.View>
  );
}
