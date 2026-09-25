import { useCurationThreads } from "../../app/useThreads";
import { ChevronDown, MapPin } from "lucide-react";
import { ResearchCurrencyControls } from "../app/useResearchCurrency";
import { useEffect, useState } from "react";
import { APIError, request } from "../../../../shared/api/client";
import { useLocale } from "../../../../shared/i18n";
import { Button, NativeSelect, NativeSelectOption, Popover, PopoverArrow, PopoverContent, PopoverTrigger } from "../../../../shared/ui";
import { usePreferences } from "../../../account/app/usePreferences";

type Settings = { schemaVersion: "vitlane.research-settings.v1"; country: "KR" | "US"; version: number };

export function ResearchSettings({ curationId, compact = false }: { curationId: string; compact?: boolean }) {
  const { l } = useLocale();
  const preferences = usePreferences(); const thread = useCurationThreads();
  const [state, setState] = useState<{ curationId: string; settings: Settings }>();
  const [working, setWorking] = useState(false);
  const [failed, setFailed] = useState(false);
  const [reload, setReload] = useState(0);
  const settings = state?.curationId === curationId ? state.settings : undefined;
  const path = `/api/v1/curations/${encodeURIComponent(curationId)}/research-settings`;
  useEffect(() => {
    let active = true;
    setFailed(false);
    void request<Settings>(path).then((result) => {
      if (active) setState({ curationId, settings: result });
    }).catch(() => { if (active) setFailed(true); });
    return () => { active = false; };
  }, [curationId, path, reload]);

  async function change(country: string) {
    if (!settings || working || thread?.busy) return;
    setWorking(true);
    setFailed(false);
    try {
      const result = await request<Settings>(path, { method: "PATCH", body: JSON.stringify({ schemaVersion: "vitlane.research-settings.v1", expectedVersion: settings.version, country }) });
      setState({ curationId, settings: result });
      await preferences.refresh();
    } catch (caught) {
      setFailed(true);
      if (caught instanceof APIError && caught.status === 409) setReload((value) => value + 1);
    } finally { setWorking(false); }
  }
  const content = <section className="curation-research-settings" aria-label={l("Research settings", "조사 설정")}>
    {import.meta.env.VITE_RESEARCH_REVIEW_STUB === "true" && <small role="status">{import.meta.env.VITE_RESEARCH_REVIEW_LIVE_AI === "true" ? l("Axis evaluation review · AI is live; Korean and US products use saved observations. Try Lamy Safari in Korea or a fountain pen in the US.", "축 평가 테스트 · AI는 실제 연결, 한국·미국 상품은 저장된 관찰값입니다. 한국에서는 라미 사파리, 미국에서는 만년필로 확인하세요.") : l("STUB review · audit observations from September 11. Try Lamy Safari or Galaxy Buds. Searches and usage are simulated; selections are saved.", "STUB 검토 · 9월 11일 실사 관찰값입니다. 라미 사파리·갤럭시 버즈로 확인하세요. 검색·사용량은 모의 값이며 선택은 저장됩니다.")}</small>}
    <label>{l("Country for the next research", "다음 조사 국가")}
      <NativeSelect aria-label={l("Country for the next research", "다음 조사 국가")} value={settings?.country ?? ""} disabled={!settings || working || Boolean(thread?.busy)} onChange={(event) => void change(event.target.value)}>
        {!settings && <NativeSelectOption value="">{l("Loading settings", "설정 불러오는 중")}</NativeSelectOption>}
        <NativeSelectOption value="KR">{l("South Korea · KR", "한국 · KR")}</NativeSelectOption>
        <NativeSelectOption value="US">{l("United States · US", "미국 · US")}</NativeSelectOption>
      </NativeSelect>
    </label>
    <small>{l("Applies when you start the next research. Current results and price limits stay as recorded.", "다음 조사를 시작할 때 적용됩니다. 현재 결과와 가격 조건은 기록된 값을 유지합니다.")}</small>
    <ResearchCurrencyControls />
    {failed && <p role="alert">{l("Settings could not be confirmed. Reload and try again.", "설정을 확인하지 못했습니다. 다시 불러온 뒤 시도해 주세요.")} <Button type="button" emphasis="quiet" onClick={() => setReload((value) => value + 1)}>{l("Reload", "다시 불러오기")}</Button></p>}
  </section>;
  if (!compact) return content;
  return <Popover><PopoverTrigger asChild>
    <Button type="button" emphasis="quiet" size="compact" className="curation-composer-settings-trigger" aria-label={l("Research and display settings", "조사·보기 설정")}>
      <MapPin size={14} aria-hidden="true" />
      <span>{settings?.country ?? l("Loading", "불러오는 중")} · {preferences.values.preferredCurrency}</span>
      <ChevronDown size={13} aria-hidden="true" />
    </Button>
  </PopoverTrigger><PopoverContent side="top" align="center" sideOffset={10} collisionPadding={12} className="curation-composer-settings-popover">{content}<PopoverArrow width={14} height={7} className="curation-popover__arrow" /></PopoverContent></Popover>;
}
