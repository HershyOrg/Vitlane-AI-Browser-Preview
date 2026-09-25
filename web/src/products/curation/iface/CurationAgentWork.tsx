import { useState } from "react";
import { CircleCheck, Circle, CircleAlert, LoaderCircle } from "lucide-react";
import { Button, Notice } from "../../../shared/ui";
import type { CurationActiveWork, IntelligenceJob } from "../domain/types";
import {
  jobStepLabel,
  jobReasonLabel,
} from "../domain/jobPresentation";
import { invariantContent, useLocale, type Localize } from "../../../shared/i18n";
import {
  cancelCurationAction,
  retryIntelligenceJob,
} from "../infra/curationApi";

/**
 * Shows what the Server is doing for this curation right now.
 *
 * ADR-0038 folded job progress into the workspace projection, so this panel is
 * purely presentational: the page's single poll is the data source, and there
 * is no work reference to discover or hold. Reloading recovers everything from
 * the same response.
 */
function activeJob(jobs: IntelligenceJob[]): IntelligenceJob | undefined {
  return (
    jobs.find(({ status }) => status === "RUNNING") ??
    jobs.find(({ status }) => status === "PENDING")
  );
}

function CancelCurationActionButton({
  job,
  onWorkChanged,
}: {
  job: IntelligenceJob;
  onWorkChanged?: () => void | Promise<void>;
}) {
  const { l } = useLocale();
  const [cancelling, setCancelling] = useState(false);
  const [cancelError, setCancelError] = useState<string | undefined>();
  return (
    <>
      {cancelError ? <p>{cancelError}</p> : null}
      <Button
        className="curation-cancel-action"
        emphasis="secondary"
        size="compact"
        type="button"
        busy={cancelling}
        onClick={() => {
          setCancelling(true);
          setCancelError(undefined);
          cancelCurationAction(job.actionId)
            .then(() => onWorkChanged?.())
            .catch(() =>
              setCancelError(
                l(
                  "We couldn't cancel the research. Check the latest status.",
                  "조사를 취소하지 못했습니다. 최신 상태를 확인해 주세요.",
                ),
              ),
            )
            .finally(() => setCancelling(false));
        }}
      >
        {l("Cancel", "취소")}
      </Button>
    </>
  );
}

export function CurationResultConfirmationPanel({
  job,
  onWorkChanged,
}: {
  job?: IntelligenceJob;
  onWorkChanged?: () => void | Promise<void>;
}) {
  const { l } = useLocale();
  return (
    <section className="curation-action-activity" role="status">
      <Notice
        tone="warning"
        announce
        title={l("Research result confirmation required", "조사 결과 확인 필요")}
      >
        <p>
          {l(
            "We couldn't confirm whether the latest research finished, so we paused it safely.",
            "최근 조사가 완료되었는지 확인하지 못해 안전하게 중단했습니다.",
          )}
        </p>
        <p>
          {l(
            "Vitlane must verify the result. Your saved recommendations are unchanged.",
            "Vitlane에서 결과를 확인해야 합니다. 저장된 추천 상품은 그대로 유지됩니다.",
          )}
        </p>
        {job ? (
          <CancelCurationActionButton
            job={job}
            onWorkChanged={onWorkChanged}
          />
        ) : null}
      </Notice>
    </section>
  );
}

// Jobs are projected oldest-first for the whole curation, so only the newest
// job can speak for the current state. An old FAILED behind a newer outcome
// must never resurface as if it just happened.
function failedJob(jobs: IntelligenceJob[]): IntelligenceJob | undefined {
  const newest = jobs.at(-1);
  return newest?.status === "FAILED" ? newest : undefined;
}

export function CurationAgentRunningPanel({
  job,
  activeWork,
  onWorkChanged,
}: {
  job: IntelligenceJob;
  activeWork?: CurationActiveWork;
  onWorkChanged?: () => void | Promise<void>;
}) {
  const { l } = useLocale();

  const steps = job.status === "PENDING" ? [] : job.steps;
  const label = job.status === "PENDING"
    ? l("Waiting to research…", "조사 대기 중…")
    : localizedActiveWork(activeWork?.label, l) ?? l("Finding recommendations", "추천 상품 조사");
  return (
    <section className="curation-action-activity is-running curation-step-activity">
      <ol className="curation-step-list" aria-label={l("Research progress", "조사 진행 상황")}>
        {steps.length === 0 ? (
          <li className="curation-step is-running" role="status" aria-atomic="true">
            <LoaderCircle className="curation-step-spinner" size={16} aria-hidden="true" />
            <span>{label}</span>
          </li>
        ) : steps.map((step, index) => (
          <li key={`${step.kind}-${step.startedAt}-${index}`} className={`curation-step is-${step.status.toLowerCase()}`}
            role={step.status === "RUNNING" ? "status" : undefined} aria-atomic={step.status === "RUNNING" ? true : undefined}>
            {step.status === "SUCCEEDED" ? <CircleCheck size={16} aria-hidden="true" />
              : step.status === "RUNNING" ? <LoaderCircle className="curation-step-spinner" size={16} aria-hidden="true" />
              : step.status === "FAILED" ? <CircleAlert size={16} aria-hidden="true" />
              : <Circle size={16} aria-hidden="true" />}
            <span>{step.status === "SUCCEEDED" ? { INTERPRETING: l("Request interpreted", "요청 해석"), SEARCHING_CATALOG: l("Catalogs researched", "상품 카탈로그 조사"), RANKING: l("Candidates compared", "후보 비교"), SUBMITTING: l("Results prepared", "결과 정리") }[step.kind] : jobStepLabel(step.kind, l)}
              {step.reasonCode && <small className="curation-step-reason">{jobReasonLabel(step.reasonCode, l)}</small>}
            </span>
          </li>
        ))}
      </ol>
      <div className="curation-step-actions"><CancelCurationActionButton job={job} onWorkChanged={onWorkChanged} /></div>
    </section>
  );
}

export function CurationAgentWork({
  jobs,
  activeWork,
  onWorkChanged,
}: {
  jobs: IntelligenceJob[];
  activeWork?: CurationActiveWork;
  // Retrying changes Server state the workspace response carries, not just
  // this panel. Without telling the page to re-read it, the failure notice
  // stays up until the next scheduled poll.
  onWorkChanged?: () => void | Promise<void>;
}) {
  const { l } = useLocale();
  const [retrying, setRetrying] = useState(false);
  const [retryError, setRetryError] = useState<string | undefined>();

  const running = activeJob(jobs);
  const failed = failedJob(jobs);

  if (activeWork?.status === "RESULT_CONFIRMATION_REQUIRED") {
    return (
      <CurationResultConfirmationPanel
        job={running}
        onWorkChanged={onWorkChanged}
      />
    );
  }

  if (running) {
    return (
      <CurationAgentRunningPanel
        job={running}
        activeWork={activeWork}
        onWorkChanged={onWorkChanged}
      />
    );
  }

  if (failed) {
    return (
      <section className="curation-action-activity is-failed" role="alert">
        <Notice tone="warning" announce title={l("Action failed", "행동을 완료하지 못했습니다")}>
          <p>{jobReasonLabel(failed.failureCode, l)}</p>
          <p>{l("Your saved recommendations are unchanged. Choose the next action below.", "저장된 추천 상품은 그대로 유지됩니다. 아래에서 다음 행동을 선택해 주세요.")}</p>
          {retryError ? <p>{retryError}</p> : null}
          {failed.retryable ? (
            <Button
              emphasis="quiet"
              type="button"
              busy={retrying}
              onClick={() => {
                setRetrying(true);
                setRetryError(undefined);
                retryIntelligenceJob(failed.jobId)
                  .then(() => onWorkChanged?.())
                  .catch(() =>
                    setRetryError(
                      l(
                        "We couldn't retry. Check again shortly.",
                        "다시 시도하지 못했습니다. 잠시 후 다시 확인해 주세요.",
                      ),
                    ),
                  )
                  .finally(() => setRetrying(false));
              }}
            >
              {l("Try again", "다시 시도")}
            </Button>
          ) : null}
        </Notice>
      </section>
    );
  }

  return null;
}

function localizedActiveWork(value: string | undefined, l: Localize) {
  if (!value) return undefined;
  if (value === invariantContent("후보 조사")) {
    return l("Finding recommendations", "추천 상품 조사");
  }
  if (value === invariantContent("조사 지능이 후보를 찾고 있습니다.")) {
    return l(
      "Vitlane is finding recommendations.",
      "Vitlane이 추천 상품을 찾고 있습니다.",
    );
  }
  if (value === invariantContent("Target 계획")) {
    return l("Preparing product groups", "상품 목록 구성");
  }
  if (value === invariantContent("조사 지능이 Target을 구성하고 있습니다.")) {
    return l(
      "Vitlane is organizing the products in your request.",
      "Vitlane이 요청하신 상품을 정리하고 있습니다.",
    );
  }
  return value;
}
