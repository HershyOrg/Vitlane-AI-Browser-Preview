/**
 * Native mapping of web/src/shared/ui/design-system/tokens.source.json.
 * Keep the Still Water semantic names so both clients make the same decisions.
 */
export const colors = {
  canvas: "#F5F8FA",
  surface: "#FFFFFF",
  surfaceSubtle: "#F1F2F5",
  surfaceSelected: "#E5EEF5",
  text: "#202124",
  textMuted: "#676B73",
  textAccent: "#1B507E",
  border: "#DFE1E6",
  borderStrong: "#B7BBC4",
  action: "#21629C",
  actionPressed: "#1B507E",
  onAction: "#FFFFFF",
  scrim: "rgba(32, 33, 36, 0.52)",
  positive: "#1E7A46",
  positiveSoft: "#E5F3EA",
  warning: "#745400",
  warningSoft: "#FFF9D9",
  danger: "#A52E3A",
  dangerSoft: "#FFF0F1",
  agentHighlight: "#8FB2D1",
  illustrationWater: "#E5EEF5",
  illustrationMoss: "#EAEFE5",
  illustrationMaple: "#F6EAE7",
  illustrationIris: "#EEEBF5",
} as const;

export const spacing = {
  0: 0,
  1: 4,
  2: 8,
  3: 12,
  4: 16,
  5: 20,
  6: 24,
  8: 32,
  12: 48,
} as const;

export const radius = {
  control: 6,
  product: 8,
  overlay: 12,
  sheet: 24,
  pill: 999,
} as const;

export const type = {
  body: 16,
  bodyLine: 24,
  helper: 13,
  helperLine: 19,
  heading: 20,
  headingLine: 28,
  hero: 28,
  heroLine: 36,
  price: 22,
  priceLine: 30,
} as const;

export const size = {
  touch: 48,
  primary: 52,
  header: 56,
  contentMax: 560,
} as const;
