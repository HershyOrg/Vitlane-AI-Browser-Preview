/**
 * Native mapping of web/src/shared/ui/design-system/tokens.source.json.
 * Keep the Still Water semantic names so both clients make the same decisions.
 */
export const colors = {
  canvas: "#F5F6FA",
  surface: "#FFFFFF",
  surfaceSubtle: "#F6F7FB",
  surfaceSelected: "#EEEDFF",
  text: "#171821",
  textMuted: "#6F7280",
  textAccent: "#5856D6",
  border: "#E7E7EF",
  borderStrong: "#C8C9D3",
  action: "#5856D6",
  actionPressed: "#4442B8",
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
  control: 16,
  product: 20,
  overlay: 18,
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
