// Reanimated entry-animation presets mirroring the smurov-blur-* CSS
// keyframes from the Electron client. RN has no CSS `filter: blur`, so
// the blur effect is replaced with a scale+opacity combo that reads
// visually similar — the motion stays, the perceptual "resolve-from-fog"
// feel is approximated via opacity + translateY/X + a tiny scale.

import { Easing, withDelay, withTiming } from "react-native-reanimated";

export const EASE = Easing.bezier(0.25, 1, 0.5, 1);

export type AnimKind = "heavy" | "med" | "light" | "fade" | "row" | "dot" | "badge";

const durMs = (kind: AnimKind): number => {
  if (kind === "heavy") return 500;
  if (kind === "med") return 450;
  if (kind === "light") return 400;
  if (kind === "row") return 300;
  if (kind === "dot") return 300;
  if (kind === "badge") return 300;
  return 400;
};

// Target translateY for each kind — matches the 8/5/3 px "lift" the CSS
// keyframes use. For `row` it's translateX; for `fade` nothing; for
// `dot` and `badge` it's a scale.
export const animConfig = (kind: AnimKind) => ({
  duration: durMs(kind),
  easing: EASE,
});

export const animDelay = (delayMs: number, value: number, cfg: ReturnType<typeof animConfig>) =>
  withDelay(delayMs, withTiming(value, cfg));
