import { budgetMinor } from "../../domain/budget";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { ArrowUp, ChevronDown, MapPin, Wallet } from "lucide-react";
import { submitChatOnEnter, Switch, Button, Popover, PopoverTrigger, PopoverContent, PopoverArrow, Input, NativeSelect, NativeSelectOption, Textarea } from "../../../../shared/ui";
import type { PlanForm } from "../domain/form";
import { useManagedRunner } from "../app/useManagedRunner";
import { useLocale } from "../../../../shared/i18n";
import { usePreferences } from "../../../account/app/usePreferences";
import type { PreferenceFields } from "../../../account/infra/preferencesApi";

type ResearchPreferencePatch = Partial<Pick<PreferenceFields, "researchCountry" | "preferredCurrency">>;
import "../../iface/budget.css";
import "../../iface/curation-thread.css";
import "./plan-creator.css";

type Props = {
  initialForm: PlanForm;
  working: boolean;
  onSubmit: (form: PlanForm) => Promise<void>;
};

// agentMode is deliberately absent. ADR-0038 retired the external Agent
// path, so MANAGED is the only owner a new plan can start with.
// Budget amounts are not remembered: every new Curation starts in Auto mode.
// Account currency seeds the form without relabeling an entered amount.
type RecentPlanPreferences = Pick<
  PlanForm,
  "planningMode" | "modelKey"
>;

const recentPreferencesKey = "vitlane.plan-preferences.v1";
const lunaModelKey = "gpt-5.6-luna";

export function PlanCreator({ initialForm, working, onSubmit }: Props) {
  const { l, locale } = useLocale();
  const preferences = usePreferences();
  const seeded = useRef<string | null>(null);
  const touched = useRef({ country: false, currency: false });
  const storageKey = preferences.storageScope ? `${recentPreferencesKey}:${preferences.storageScope}` : recentPreferencesKey;
  const [form, setForm] = useState(() => withRecentPreferences(initialForm, storageKey));
  const [dialog, setDialog] = useState<"BUDGET" | "RESEARCH">();
  const [savingSettings, setSavingSettings] = useState(false);
  const [settingsError, setSettingsError] = useState(false);
  const activeScope = useRef(storageKey);
  const accountEpoch = useRef(0);
  if (activeScope.current !== storageKey) { activeScope.current = storageKey; accountEpoch.current += 1; }
  const formScope = useRef(storageKey);
  useEffect(() => () => { accountEpoch.current += 1; }, []);
  const saveInFlight = useRef(false);
  const pendingPreferences = useRef<ResearchPreferencePatch>({});
  const { capability } = useManagedRunner();

  // null means the capability is still loading; only an explicit server-side
  // disable blocks submission (ADR-0038 leaves no fallback owner).
  const managedDisabled = capability?.enabled === false;
  const serverExhausted = capability?.serverExhausted === true;

  const budgetEnabled = form.amount !== "";
  const budgetValid = !budgetEnabled || (budgetMinor(form.amount, form.currency) ?? 0n) > 0n;
  const settingsPending = savingSettings || settingsError;

  useEffect(() => {
    if (!capability) return;
    setForm((current) => {
      // ADR-0038 retired the external Agent path: MANAGED is the only owner,
      // and when the Server disables it submission is blocked instead of
      // falling back.
      // A remembered model the Server no longer offers would submit a MANAGED
      // plan against a model that cannot run, so it is replaced rather than
      // kept: only a key that is still on offer survives.
      const isOffered = (modelKey: string) =>
        capability.models.some(({ key }) => key === modelKey);
      const modelKey = isOffered(current.modelKey)
        ? current.modelKey
        : isOffered(lunaModelKey)
          ? lunaModelKey
          : isOffered(capability.defaultModelKey)
            ? capability.defaultModelKey
            : (capability.models[0]?.key ?? "");
      if (current.agentMode === "MANAGED" && modelKey === current.modelKey) {
        return current;
      }
      return { ...current, agentMode: "MANAGED", modelKey };
    });
  }, [capability]);

  useEffect(() => {
    const changedAccount = formScope.current !== storageKey;
    if (changedAccount) {
      formScope.current = storageKey;
      seeded.current = null;
      touched.current = { country: false, currency: false };
      saveInFlight.current = false;
      pendingPreferences.current = {};
      setSavingSettings(false); setSettingsError(false); setDialog(undefined);
      setForm(withRecentPreferences(initialForm, storageKey));
    }
    if (!preferences.ready || seeded.current === storageKey) return;
    seeded.current = storageKey;
    if (initialForm.originalIntent.trim()) return;
    const restored = changedAccount ? withRecentPreferences(initialForm, storageKey) : null;
    setForm((previous) => {
      const current = restored ?? previous;
      const currency = touched.current.currency || current.amount ? current.currency : preferences.values.preferredCurrency;
      return { ...current, country: touched.current.country ? current.country : preferences.values.researchCountry, currency, displayCurrency: current.displayCurrency ?? preferences.values.preferredCurrency };
    });
  }, [preferences.ready, preferences.values, initialForm, storageKey]);

  function update<K extends keyof PlanForm>(key: K, value: PlanForm[K]) {
    setForm((current) => ({ ...current, [key]: value }));
    if (key === "country" && (value === "KR" || value === "US")) {
      touched.current.country = true;
    }
    if (key === "currency" && (value === "KRW" || value === "USD")) {
      touched.current.currency = true;
    }
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!budgetValid || working || saveInFlight.current || settingsPending || managedDisabled || serverExhausted || !preferences.ready || !form.originalIntent.trim()) return;
    await onSubmit({ ...form, planningMode: form.controlMode==="MANUAL"?"SINGLE":"AUTO", budgetAllocationMode:form.controlMode==="MANUAL"?"EQUAL":"AUTO" });
    rememberPreferences(form, storageKey);
    void preferences.refresh().catch(() => {});
  }


  function openDialog(kind: "BUDGET" | "RESEARCH") {
    if (saveInFlight.current) return;
    setDialog(kind);
  }

  function updateBudget(patch: Partial<Pick<PlanForm, "amount" | "currency">>) {
    touched.current.currency = true;
    setForm(current => ({ ...current, ...patch, budgetAllocationMode: "EQUAL", budgetExplicit: true }));
  }

  async function saveResearchPreferences(patch: ResearchPreferencePatch) {
    if (saveInFlight.current || !preferences.ready) return;
    const epoch = accountEpoch.current;
    const desired = { ...pendingPreferences.current, ...patch };
    pendingPreferences.current = desired;
    if (desired.researchCountry) touched.current.country = true;
    saveInFlight.current = true;
    setSavingSettings(true);
    setSettingsError(false);
    setForm(current => ({ ...current,
      ...(desired.researchCountry ? { country: desired.researchCountry, city: "" } : {}),
      ...(desired.preferredCurrency ? { displayCurrency: desired.preferredCurrency } : {}),
    }));
    try {
      await preferences.save(desired);
      if (accountEpoch.current !== epoch) return;
      pendingPreferences.current = {};
      if (desired.preferredCurrency) {
        const currency = desired.preferredCurrency;
        setForm(current => ({ ...current, ...(!current.budgetExplicit && !current.amount ? { currency } : {}) }));
      }
    } catch {
      if (accountEpoch.current === epoch) setSettingsError(true);
    } finally {
      if (accountEpoch.current === epoch) {
        saveInFlight.current = false;
        setSavingSettings(false);
      }
    }
  }

  function settingsPopover(kind: "BUDGET" | "RESEARCH") { return (
      <PopoverContent side="top" align="center" sideOffset={10} collisionPadding={12} className="budget-popover init-settings" aria-label={kind === "BUDGET" ? l("Budget", "예산") : l("Research and display", "조사·보기")} onCloseAutoFocus={event => {
        // Closing can finish after the user has focused the composer or another
        // setting. Restore only lost focus, never steal a newer focus choice.
        if (document.activeElement !== document.body && document.activeElement?.isConnected) event.preventDefault();
      }}>
      <div className="budget-editor">
        <div className="budget-editor__heading"><strong>{kind === "BUDGET" ? l("Budget", "예산") : l("Research and display", "조사·보기")}</strong></div>
        {kind === "BUDGET" && <><div className="curation-mode-switch"><span>{l("Manual", "수동")}</span><Switch aria-label={l("Automatic curation", "자동 큐레이션")} checked={form.controlMode !== "MANUAL"} onCheckedChange={auto => setForm(current => ({ ...current, controlMode: auto ? "AUTO" : "MANUAL", budgetExplicit: !auto, budgetAllocationMode: auto ? "AUTO" : "EQUAL", amount: auto && !budgetValid ? "" : current.amount }))} /><span>{l("Auto", "자동")}</span></div>{form.controlMode !== "MANUAL" && <p>{l("The budget is determined automatically.", "예산이 자동으로 결정됩니다.")}</p>}</>}
        {(kind !== "BUDGET" || form.controlMode === "MANUAL") && <div className="init-settings__row">
          {kind === "BUDGET" ? <>
            <label>{l("Total budget", "총 예산")}<Input aria-label={l("Total budget", "총 예산")} inputMode="decimal" value={form.amount} onChange={event => updateBudget({ amount: event.target.value.trim() })} placeholder={l("No limit", "제한 없음")} /></label>
            <label>{l("Budget currency", "예산 통화")}<NativeSelect id="curation-currency" aria-label={l("Budget currency", "예산 통화")} value={form.currency} onChange={event => updateBudget({ currency: event.target.value, amount: "" })}><NativeSelectOption value="KRW">{l("KRW", "KRW")}</NativeSelectOption><NativeSelectOption value="USD">{l("USD", "USD")}</NativeSelectOption></NativeSelect></label>
          </> : <>
            <label>{l("Research country", "조사 국가")}<NativeSelect id="shipping-country" aria-label={l("Research country", "조사 국가")} disabled={savingSettings || !preferences.ready} value={form.country} onChange={event => void saveResearchPreferences({ researchCountry: event.target.value === "US" ? "US" : "KR" })}><NativeSelectOption value="KR">{l("South Korea · KR", "한국 · KR")}</NativeSelectOption><NativeSelectOption value="US">{l("United States · US", "미국 · US")}</NativeSelectOption></NativeSelect></label>
            <label>{l("Display currency", "표시 통화")}<NativeSelect aria-label={l("Display currency", "표시 통화")} disabled={savingSettings || !preferences.ready} value={form.displayCurrency ?? preferences.values.preferredCurrency} onChange={event => void saveResearchPreferences({ preferredCurrency: event.target.value === "USD" ? "USD" : "KRW" })}><NativeSelectOption value="KRW">{l("KRW", "KRW")}</NativeSelectOption><NativeSelectOption value="USD">{l("USD", "USD")}</NativeSelectOption></NativeSelect></label>
          </>}
        </div>}
        {kind === "BUDGET" && form.controlMode==="MANUAL" && <div className="init-settings__unlimited">
            <Button type="button" emphasis="secondary" size="compact" aria-pressed={!form.amount} onClick={() => updateBudget({ amount: "" })}>{l("No limit", "제한 없음")}</Button>
        </div>}
      </div><PopoverArrow width={14} height={7} className="budget-popover__arrow" />
      </PopoverContent>
  ); }
  return <form className="shell-request-form init-request-form" onSubmit={event => void submit(event)}>
    <h1 className="init-request-form__title">{l("What are you looking for?", "무엇을 찾고 있나요?")}</h1>
    <section className="shell-intent-composer init-composer" aria-label={l("Product search request", "상품 찾기 요청")}>
      <div className="init-composer__settings">
        <Popover open={dialog === "BUDGET"} onOpenChange={open => { if (!open) setDialog(undefined); }}>
          <PopoverTrigger asChild>
        <Button type="button" emphasis="quiet" size="compact" disabled={savingSettings} onClick={() => openDialog("BUDGET")} aria-label={l("Budget settings", "예산 설정")}>
          <Wallet size={14} aria-hidden="true" /><span>{l("Budget", "예산")} · {form.controlMode!=="MANUAL" ? l("Auto","자동") : budgetEnabled && budgetValid ? new Intl.NumberFormat(locale, { style: "currency", currency: form.currency }).format(Number(form.amount)) : budgetEnabled ? l("Check amount", "금액 확인") : l("No limit", "제한 없음")}</span><ChevronDown size={13} aria-hidden="true" />
        </Button>
          </PopoverTrigger>
          {dialog === "BUDGET" && settingsPopover("BUDGET")}
        </Popover>
        <Popover open={dialog === "RESEARCH"} onOpenChange={open => { if (!open) setDialog(undefined); }}>
          <PopoverTrigger asChild>
        <Button type="button" emphasis="quiet" size="compact" disabled={savingSettings} onClick={() => openDialog("RESEARCH")} aria-label={l("Research and display settings", "조사·보기 설정")}>
          <MapPin size={14} aria-hidden="true" /><span>{form.country} · {form.displayCurrency ?? preferences.values.preferredCurrency}</span><ChevronDown size={13} aria-hidden="true" />
        </Button>
          </PopoverTrigger>
          {dialog === "RESEARCH" && settingsPopover("RESEARCH")}
        </Popover>
      </div>
      <div className="init-composer__input-frame">
      <Textarea id="curation-intent" onKeyDown={submitChatOnEnter} rows={3} value={form.originalIntent} disabled={working || savingSettings} onChange={event => update("originalIntent", event.target.value)} aria-label={l("Products to find", "찾을 상품")} placeholder={l("Enter the product you're looking for", "찾고 있는 상품을 입력해주세요")} />
      <footer className="shell-intent-composer__footer init-composer__footer">
        {capability && capability.models.length > 0 && <NativeSelect className="init-composer__model" disabled={savingSettings} value={form.modelKey} aria-label={l("Research model", "조사 모델")} onChange={event => update("modelKey", event.target.value)}>
          {capability.models.map(model => <NativeSelectOption key={model.key} value={model.key}>{model.label}</NativeSelectOption>)}
        </NativeSelect>}
        <Button className="shell-intent-composer__submit" type="submit" emphasis="primary" busy={working} disabled={settingsPending || !preferences.ready || !form.originalIntent.trim() || !budgetValid || managedDisabled || serverExhausted} aria-label={l("Start product search", "상품 찾기 시작")}><ArrowUp aria-hidden="true" /></Button>
      </footer>
      </div>
      {!budgetValid && <p role="alert">{l("Enter a positive amount in the currency's smallest unit.", "통화의 최소 단위에 맞는 양수 금액을 입력해 주세요.")}</p>}
      {savingSettings && <p role="status">{l("Saving settings…", "설정을 저장하고 있습니다…")}</p>}
      {settingsError && <p role="alert">{l("Settings could not be saved.", "설정을 저장하지 못했습니다.")} <Button type="button" emphasis="quiet" size="compact" onClick={() => void saveResearchPreferences(pendingPreferences.current)}>{l("Retry", "다시 시도")}</Button></p>}
      {managedDisabled && <p role="alert">{l("Research is temporarily unavailable.", "현재 조사를 시작할 수 없습니다.")}</p>}
      {serverExhausted && <p role="alert">{l("Today's AI usage limit has been reached.", "오늘의 AI 사용 한도에 도달했습니다.")}</p>}
    </section>

  </form>;
}

function withRecentPreferences(initial: PlanForm, storageKey: string): PlanForm {
  const fixed = {
    ...initial,
    controlMode: "AUTO" as const,
    budgetExplicit:false,
    planningMode: "AUTO" as const,
    budgetAllocationMode: "AUTO" as const,
    city: "",
    executionMode: "EXPERIMENT" as const,
    agentMode: "MANAGED" as const,
    // Model preference is presentation state. A first-time or storage-less
    // browser starts on Luna without making the Server default authoritative
    // over the user's selector.
    modelKey: lunaModelKey,
  };
  if (typeof window === "undefined" || initial.originalIntent.trim()) return fixed;
  try {
    const stored = window.localStorage.getItem(storageKey);
    if (!stored) return fixed;
    const parsed = JSON.parse(stored) as Partial<RecentPlanPreferences>;
    return {
      ...fixed,
      planningMode: "AUTO",
      city: "",
      modelKey:
        typeof parsed.modelKey === "string" && parsed.modelKey.trim()
          ? parsed.modelKey.trim()
          : fixed.modelKey,
    };
  } catch {
    return fixed;
  }
}

function rememberPreferences(form: PlanForm, storageKey: string) {
  if (typeof window === "undefined") return;
  const preferences: RecentPlanPreferences = {
    planningMode: form.planningMode,
    modelKey: form.modelKey,
  };
  try {
    window.localStorage.setItem(
      storageKey,
      JSON.stringify(preferences),
    );
  } catch {
    // A storage policy or quota failure must not turn a successful plan
    // submission into a rejected UX promise. The next visit falls back to
    // Luna instead.
  }
}
