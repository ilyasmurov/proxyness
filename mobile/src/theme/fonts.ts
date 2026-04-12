// Font family keys matching what the Electron client uses:
//   fd = display       → Barlow Semi Condensed (headings, labels, buttons)
//   fb = body          → Figtree (body copy, descriptions)
//   fm = mono/numeric  → Barlow (metrics, monospace-feeling numbers)

export const fonts = {
  display: "BarlowSemiCondensed_600SemiBold",
  displayBold: "BarlowSemiCondensed_700Bold",
  displayRegular: "BarlowSemiCondensed_400Regular",
  displayLight: "BarlowSemiCondensed_300Light",
  body: "Figtree_400Regular",
  bodyMedium: "Figtree_500Medium",
  bodySemiBold: "Figtree_600SemiBold",
  mono: "Barlow_500Medium",
  monoSemiBold: "Barlow_600SemiBold",
} as const;

export type FontKey = keyof typeof fonts;
