import {
  createContext,
  type ReactNode,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { Check, Moon, Palette, Sun } from "lucide-react";
import {
  ToggleGroup,
  ToggleGroupItem,
} from "./primitives/toggle-group";
import { useLocale } from "../i18n/LocaleProvider";
import type { MessageKey } from "../i18n/catalog";

export type AppearanceTheme = "light" | "dark";
// "blue" is the stored identifier of the Still Water accent (ADR-0073) and
// "neutral" the black alternative; "moss", "maple" and "iris" are the palettes
// of ADR-0082. The order is the order of the dots in the picker.
export const appearanceAccents = [
  "blue",
  "moss",
  "maple",
  "iris",
  "neutral",
] as const;
export type AppearanceAccent = (typeof appearanceAccents)[number];

export type Appearance = {
  theme: AppearanceTheme;
  accent: AppearanceAccent;
};

type AppearanceContextValue = Appearance & {
  setTheme: (theme: AppearanceTheme) => void;
  setAccent: (accent: AppearanceAccent) => void;
};

// v2 (ADR-0073, Still Water): every user starts on the still accent once. A v1
// record only carries its theme over; its accent is discarded. The palette ids
// of ADR-0082 live in the same record; an unknown id reads as still.
const appearanceStorageKey = "vitlane.appearance.v2";
const legacyAppearanceStorageKey = "vitlane.appearance.v1";
const defaultAppearance: Appearance = {
  theme: "light",
  accent: "blue",
};

// Palette names are shown in English in every locale (owner 2026-09-17).
const accentLabels: Record<
  AppearanceAccent,
  { name: MessageKey; accent: MessageKey }
> = {
  blue: { name: "appearance.water", accent: "appearance.waterAccent" },
  moss: { name: "appearance.moss", accent: "appearance.mossAccent" },
  maple: { name: "appearance.maple", accent: "appearance.mapleAccent" },
  iris: { name: "appearance.iris", accent: "appearance.irisAccent" },
  neutral: { name: "appearance.ink", accent: "appearance.inkAccent" },
};

export function isAppearanceAccent(value: unknown): value is AppearanceAccent {
  return (
    typeof value === "string" &&
    (appearanceAccents as readonly string[]).includes(value)
  );
}

const AppearanceContext = createContext<AppearanceContextValue | null>(null);

export function AppearanceProvider({ children }: { children: ReactNode }) {
  const [appearance, setAppearance] = useState(readAppearance);

  useEffect(() => {
    applyAppearance(appearance);
    try {
      window.localStorage.setItem(
        appearanceStorageKey,
        JSON.stringify(appearance),
      );
    } catch {
      // Presentation preferences remain usable even when storage is blocked.
    }
  }, [appearance]);

  const value = useMemo<AppearanceContextValue>(
    () => ({
      ...appearance,
      setTheme: (theme) =>
        setAppearance((current) => ({ ...current, theme })),
      setAccent: (accent) =>
        setAppearance((current) => ({ ...current, accent })),
    }),
    [appearance],
  );

  return (
    <AppearanceContext.Provider value={value}>
      {children}
    </AppearanceContext.Provider>
  );
}

export function useAppearance(): AppearanceContextValue {
  const context = useContext(AppearanceContext);
  if (!context) {
    throw new Error("useAppearance must be used within AppearanceProvider");
  }
  return context;
}

export function AppearanceControls({
  compact = false,
}: {
  compact?: boolean;
}) {
  const { accent, setAccent, setTheme, theme } = useAppearance();
  const { t } = useLocale();
  const caption =
    accent === "blue"
      ? `${t(accentLabels[accent].name)} · ${t("appearance.default")}`
      : t(accentLabels[accent].name);

  return (
    <section
      className="vt-appearance-controls"
      aria-label={t("appearance.title")}
      data-compact={compact || undefined}
    >
      <div className="vt-appearance-controls__heading">
        <Palette aria-hidden="true" />
        <span>
          <strong>{t("appearance.title")}</strong>
          <small>{t("common.savedInBrowser")}</small>
        </span>
      </div>

      <div className="vt-appearance-controls__row">
        <span>{t("appearance.theme")}</span>
        <ToggleGroup
          aria-label={t("appearance.themeSelection")}
          className="vt-appearance-controls__toggle"
          type="single"
          variant="outline"
          spacing={0}
          value={theme}
          onValueChange={(value) => {
            if (value === "light" || value === "dark") setTheme(value);
          }}
        >
          <ToggleGroupItem value="light" aria-label={t("appearance.lightMode")}>
            <Sun aria-hidden="true" />
            <span>{t("appearance.light")}</span>
            {theme === "light" && <Check aria-hidden="true" />}
          </ToggleGroupItem>
          <ToggleGroupItem value="dark" aria-label={t("appearance.darkMode")}>
            <Moon aria-hidden="true" />
            <span>{t("appearance.dark")}</span>
            {theme === "dark" && <Check aria-hidden="true" />}
          </ToggleGroupItem>
        </ToggleGroup>
      </div>

      {/* Five palette dots and one caption line (ADR-0082). A dot carries no
          text; the selected dot shows an inner mark and the caption names it. */}
      <div className="vt-appearance-controls__row">
        <span>{t("appearance.accent")}</span>
        <ToggleGroup
          aria-label={t("appearance.accentSelection")}
          className="vt-appearance-controls__dots"
          type="single"
          spacing={2}
          value={accent}
          onValueChange={(value) => {
            if (isAppearanceAccent(value)) setAccent(value);
          }}
        >
          {appearanceAccents.map((id) => (
            <ToggleGroupItem
              key={id}
              value={id}
              aria-label={t(accentLabels[id].accent)}
            >
              <span
                className={`vt-appearance-controls__swatch is-${id}`}
                aria-hidden="true"
              />
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <small className="vt-appearance-controls__caption">{caption}</small>
      </div>
    </section>
  );
}

function readAppearance(): Appearance {
  if (typeof document === "undefined") return defaultAppearance;

  const root = document.documentElement;
  let stored: Partial<Appearance> = {};
  let legacy: Partial<Appearance> = {};
  try {
    stored = JSON.parse(
      window.localStorage.getItem(appearanceStorageKey) ?? "{}",
    ) as Partial<Appearance>;
    legacy = JSON.parse(
      window.localStorage.getItem(legacyAppearanceStorageKey) ?? "{}",
    ) as Partial<Appearance>;
  } catch {
    // Invalid or unavailable storage falls back to the documented defaults.
  }
  const storedTheme = stored.theme ?? legacy.theme;
  const theme = root.dataset.theme === "dark" || root.dataset.theme === "light"
    ? root.dataset.theme
    : storedTheme === "dark" || storedTheme === "light"
      ? storedTheme
      : defaultAppearance.theme;
  // Only a v2 record can opt into another accent than still.
  const accent = isAppearanceAccent(root.dataset.accent)
    ? root.dataset.accent
    : isAppearanceAccent(stored.accent)
      ? stored.accent
      : defaultAppearance.accent;
  return { theme, accent };
}

function applyAppearance({ accent, theme }: Appearance) {
  const root = document.documentElement;
  root.classList.toggle("dark", theme === "dark");
  root.dataset.theme = theme;
  root.dataset.accent = accent;
}
