import { Check, Languages } from "lucide-react";
import { ToggleGroup, ToggleGroupItem } from "../ui/primitives/toggle-group";
import { useLocale, type UILocale } from "./LocaleProvider";

export function LanguageControls({ compact = false }: { compact?: boolean }) {
  const { locale, setLocale, t } = useLocale();
  return (
    <section className="vt-appearance-controls" aria-label={t("language.title")} data-compact={compact || undefined}>
      <div className="vt-appearance-controls__heading">
        <Languages aria-hidden="true" />
        <span><strong>{t("language.title")}</strong><small>{t("language.description")}</small></span>
      </div>
      <div className="vt-appearance-controls__row">
        <span>{t("language.title")}</span>
        <ToggleGroup
          aria-label={t("language.selection")}
          className="vt-appearance-controls__toggle"
          type="single"
          variant="outline"
          spacing={0}
          value={locale}
          onValueChange={(value) => {
            if (value === "en-US" || value === "ko-KR") setLocale(value as UILocale);
          }}
        >
          <ToggleGroupItem value="en-US"><span>{t("language.english")}</span>{locale === "en-US" && <Check aria-hidden="true" />}</ToggleGroupItem>
          <ToggleGroupItem value="ko-KR"><span>{t("language.korean")}</span>{locale === "ko-KR" && <Check aria-hidden="true" />}</ToggleGroupItem>
        </ToggleGroup>
      </div>
    </section>
  );
}
