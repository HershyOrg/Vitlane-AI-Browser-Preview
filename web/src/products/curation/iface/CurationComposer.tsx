import { ArrowUp, Check, ChevronDown, LoaderCircle, MapPin, Plus, Search, Sparkles } from "lucide-react";
import { useEffect, useState, type FormEvent, type ReactNode, type Ref } from "react";
import { submitChatOnEnter, Switch, Button, Popover, PopoverArrow, PopoverContent, PopoverTrigger, Textarea } from "../../../shared/ui";
import { useLocale, type Localize } from "../../../shared/i18n";
import { useCurationThreads } from "../app/useThreads";
import type { PlanTarget } from "../../../shared/api/types";

export type Focus =
  | { kind: "AUTO" }
  | { kind: "ADD_TARGET" }
  | { kind: "RESEARCH_AGAIN"; target: PlanTarget }
 | {kind:"RETRY"; jobId:string; title:string};

export function CurationComposer({ country, contextSettings, contextAction, focus, selectFocus, busy,
  modeSelectorOpen, setModeSelectorOpen, autoResolutionMessage, researchTargets = [],
  composerRef, instruction, setInstruction, submitComposer, retryJobs=[], unavailableTargets=[],
}: {
  retryJobs?: {jobId:string;title:string}[];
 unavailableTargets?: string[];
 country: string;
  contextAction?: ReactNode;
  contextSettings?: ReactNode;
  focus: Focus;
  selectFocus: (focus: Focus) => void;
  busy: boolean;
  modeSelectorOpen: boolean;
  setModeSelectorOpen: (open: boolean) => void;
  autoResolutionMessage?: string;
  researchTargets?: PlanTarget[];
  composerRef: Ref<HTMLTextAreaElement>;
  instruction: string;
  setInstruction: (value: string) => void;
  submitComposer: (event: FormEvent<HTMLFormElement>) => void;
}) {
  const { l } = useLocale();
 const [step,setStep]=useState<"ACTION"|"TARGET"|"RETRY">("ACTION");
 const threads=useCurationThreads();const [draftAuto,setDraftAuto]=useState(true);const [draftFocus,setDraftFocus]=useState<Focus>(focus);const [modeError,setModeError]=useState(false);
 useEffect(()=>{if(modeSelectorOpen){setDraftAuto(threads?.mode.mode!=="MANUAL");setDraftFocus(focus.kind==="AUTO"?{kind:"ADD_TARGET"}:focus);setModeError(false)}},[modeSelectorOpen,threads?.mode.mode]);
 const menuFocus=threads?draftFocus:focus;
 const chooseFocus=(next:Focus)=>threads?setDraftFocus(next):selectFocus(next);
 async function saveMode(){if(!threads)return;try{await threads.changeMode(draftAuto?"AUTO":"MANUAL");selectFocus(draftAuto?{kind:"AUTO"}:draftFocus);setModeSelectorOpen(false)}catch{setModeError(true)}}

  return (
    <form className="catalog-ui-focus-composer" onSubmit={submitComposer}>
      <div className="catalog-ui-focus-composer__context">
        {contextSettings ?? <span><MapPin size={14} aria-hidden="true" /> {l("Initial research country", "최초 조사 국가")} <strong>{country}</strong></span>}
        {contextAction}
      </div>
      <div className="catalog-ui-focus-composer__entry">
        <Textarea
          ref={composerRef}
          rows={1}
          maxLength={2000}
          value={instruction}
          disabled={busy || focus.kind==="RETRY"}
          aria-label={l("Research request", "조사 요청")}
          placeholder={composerPlaceholder(focus, l)}
          onChange={(event) => setInstruction(event.target.value)}
          onKeyDown={submitChatOnEnter}
        />

        <Popover open={modeSelectorOpen} onOpenChange={open=>{setModeSelectorOpen(open);if(!open)setStep("ACTION");}}>
          <PopoverTrigger asChild>
            <Button
              type="button"
              className="catalog-ui-focus-token"
              emphasis="quiet"
              disabled={busy}
              aria-label={l("Research mode: {mode}. Choose another mode", "조사 방식: {mode}. 다른 방식 선택", { mode: focusLabel(focus, l) })}
            >
              <span>{threads?.mode.mode==="AUTO" ? l("Auto","Auto") : focusLabel(focus,l)}</span>
              <ChevronDown size={13} aria-hidden="true" />
            </Button>
          </PopoverTrigger>
          <PopoverContent
            align="start"
            side="top"
            sideOffset={8}
            collisionPadding={12}
            className="catalog-ui-mode-selector"
          >
            <div className="catalog-ui-mode-selector__header">
              <div>
                <strong>{step==="ACTION" ? l("Choose an action", "행동 선택") : l("Choose a product", "대상 선택")}</strong>
                <span>{l("Choose how this request should continue your research.", "이 요청으로 조사를 어떻게 이어갈지 선택하세요.")}</span>
              </div>
              <Button
                type="button"
                size="compact"
                emphasis="quiet"
                onClick={() => setModeSelectorOpen(false)}
              >
                {l("Close", "닫기")}
              </Button>
            </div>
            {autoResolutionMessage ? (
              <p className="catalog-ui-mode-selector__message" role="status">
                {autoResolutionMessage}
              </p>
            ) : null}
            {threads && <div className="catalog-ui-mode-selector__mode">
              <div className="curation-mode-switch">
                <span>{l("Manual", "수동")}</span>
                <Switch aria-label={l("Curation Auto", "큐레이션 Auto")} checked={draftAuto} disabled={busy} onCheckedChange={setDraftAuto} />
                <span>{l("Auto", "자동")}</span>
              </div>
              <p className="catalog-ui-mode-selector__description">{draftAuto
                ? l("Actions, targets and budgets are determined from your request.", "요청에 따라 행동, 대상과 예산을 자동으로 결정합니다.")
                : l("Choose the action and target yourself.", "행동과 대상을 직접 선택합니다.")}</p>
            </div>}
            {(!threads || !draftAuto) && <div className="catalog-ui-mode-selector__choices">
              {step !== "ACTION" && <Button className="catalog-ui-mode-selector__back" type="button" size="compact" emphasis="quiet" onClick={() => setStep("ACTION")}>{l("Back", "뒤로")}</Button>}
              <div
                className="catalog-ui-mode-selector__options"
                role="listbox"
                aria-label={l("Research modes", "조사 방식 목록")}
              >
              {step==="ACTION" && <>{!threads && <ResearchModeOption
                selected={menuFocus?.kind === "AUTO"}
                icon={<Sparkles aria-hidden="true" />}
                title={l("Auto", "Auto")}
                description={l("Interpret the request and add or revisit a product.", "요청을 해석해 상품을 추가하거나 다시 조사합니다.")}
                onClick={() => chooseFocus({ kind: "AUTO" })}
              />}
              <ResearchModeOption
                selected={menuFocus?.kind === "ADD_TARGET"}
                icon={<Plus aria-hidden="true" />}
                title={l("Add product", "상품 추가")}
                description={l("Add a new product to research.", "조사할 새 상품을 추가합니다.")}
                onClick={() => chooseFocus({ kind: "ADD_TARGET" })}
              />
              <ResearchModeOption selected={menuFocus.kind==="RESEARCH_AGAIN"} icon={<Search aria-hidden="true" />} title={l("Research again","재조사")} description={l("Choose an existing product next.","다음 단계에서 기존 상품을 선택하세요.")} onClick={()=>setStep("TARGET")} />
              {retryJobs.length>0 && <ResearchModeOption selected={menuFocus.kind==="RETRY"} icon={<Search aria-hidden="true" />} title={l("Retry failed research","실패한 조사 재시도")} description={l("Continue a failed job.","실패한 작업을 다시 시도합니다.")} onClick={()=>setStep("RETRY")} />}</>}
              {step==="RETRY" && retryJobs.map(job=><ResearchModeOption key={job.jobId} selected={menuFocus.kind==="RETRY" && menuFocus.kind==="RETRY" && menuFocus.jobId===job.jobId} icon={<Search aria-hidden="true" />} title={job.title} description={l("Retry this failed job","이 작업 다시 시도")} onClick={()=>{chooseFocus({kind:"RETRY",...job});setStep("ACTION");}} />)}
              {step==="TARGET" && researchTargets
                .filter((target) => !target.removedAt)
                .map((target) => (
                  <ResearchModeOption
                    key={target.id}
                    disabled={unavailableTargets.includes(target.id)}
 selected={menuFocus?.kind === "RESEARCH_AGAIN" && menuFocus.kind==="RESEARCH_AGAIN" && menuFocus.target.id === target.id}
                    icon={<Search aria-hidden="true" />}
                    title={l("Research again · {title}", "재조사 · {title}", { title: target.title })}
                    description={unavailableTargets.includes(target.id) ? l("The 50-product limit has been reached.","상품 50개 한도에 도달했어요.") : l("Find more products using the current criteria. Feedback is optional.", "현재 기준으로 상품을 더 찾습니다. 피드백은 선택 사항입니다.")}
                    onClick={() => {chooseFocus({ kind: "RESEARCH_AGAIN", target });setStep("ACTION");}}
                  />
                ))}
              </div>
            </div>}
            {threads && <div className="catalog-ui-mode-selector__footer">
              {modeError && <p className="catalog-ui-mode-selector__error" role="alert">{l("Mode could not be saved. Try again.", "모드를 저장하지 못했습니다. 다시 시도해 주세요.")}</p>}
              <Button className="catalog-ui-mode-selector__apply" type="button" emphasis="primary" disabled={busy} onClick={() => void saveMode()}>{l("Apply", "적용")}</Button>
            </div>}
            <PopoverArrow width={14} height={7} className="curation-popover__arrow" />
          </PopoverContent>
        </Popover>
        <Button
          type="submit"
          className="catalog-ui-focus-composer__send"
          emphasis="primary"
          aria-label={l("Send research request", "조사 요청 보내기")}
          disabled={busy || (!instruction.trim() && focus.kind !== "RESEARCH_AGAIN" && focus.kind !== "RETRY")}
        >
          {busy ? (
            <LoaderCircle className="catalog-ui-spin" aria-hidden="true" />
          ) : (
            <ArrowUp aria-hidden="true" />
          )}
        </Button>
      </div>
    </form>
  );
}

function ResearchModeOption({
  selected,
  icon,
  title,
  description,
  onClick, disabled=false,
}: {
  disabled?: boolean;
 selected: boolean;
  icon: ReactNode;
  title: string;
  description: string;
  onClick: () => void;
}) {
  return (
    <Button
      type="button"
      className="catalog-ui-mode-selector__option"
      emphasis="quiet"
      role="option"
      aria-selected={selected}
      onClick={onClick}
 disabled={disabled}
    >
      <span className="catalog-ui-mode-selector__icon">{icon}</span>
      <span className="catalog-ui-mode-selector__copy">
        <strong>{title}</strong>
        <small>{description}</small>
      </span>
      {selected ? <Check className="catalog-ui-mode-selector__check" aria-hidden="true" /> : null}
    </Button>
  );
}

export function focusLabel(value: Focus, l: Localize) {
  if (value.kind === "RETRY") return l("Retry · {title}","재시도 · {title}",{title:value.title});
 if (value.kind === "AUTO") return l("Auto", "Auto");
  if (value.kind === "ADD_TARGET") return l("Add product", "상품 추가");
  return l("Research again · {title}", "재조사 · {title}", {
    title: value.target.title,
  });
}

function composerPlaceholder(value: Focus, l: Localize) {
 if(value.kind==="RETRY")return l("Send to retry this job","보내기를 눌러 이 작업을 재시도하세요");
  if (value.kind === "AUTO") {
    return l("Use and budget are enough. e.g. Running shoes for a daily 5K under $150", "용도와 예산만 말해 주세요. 예: 매일 5km를 뛸 러닝화를 150달러 안에서");
  }
  if (value.kind === "ADD_TARGET") {
    return l("Describe a new product to research", "새로 조사할 상품을 설명하세요");
  }
  return l("Describe what to refine for {title}", "{title}에서 보완할 내용을 입력하세요", {
    title: value.target.title,
  });
}
