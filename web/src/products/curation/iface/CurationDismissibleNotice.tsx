import { useState } from "react";
import { sourceLabel } from "../domain/sourceLabels";
import { X } from "lucide-react";
import { Button, Notice, type NoticeProps } from "../../../shared/ui";
import { useLocale } from "../../../shared/i18n";
import type { SourceCoverage } from "../research/infra/liveCatalogReviewApi";
import "./curation-notice.css";
import { isAmazonAdmissionUnavailable } from "../domain/sourceProduct";

type Props = Omit<NoticeProps, "announce"> & {
  noticeId: string;
  dismissLabel?: string;
};

/** Dismissal belongs to this visible event, not to the underlying failure. */
export function CurationDismissibleNotice({ noticeId, ...props }: Props) {
  return <NoticeInstance key={noticeId} {...props} />;
}

function NoticeInstance({ dismissLabel, className, ...props }: Omit<Props, "noticeId">) {
  const { l } = useLocale();
  const [dismissed, setDismissed] = useState(false);
  if (dismissed) return null;
  return <div className="curation-dismissible-notice">
    <Notice {...props} announce className={`curation-dismissible-notice__banner ${className ?? ""}`} />
    <Button className="curation-dismissible-notice__close" emphasis="quiet"
      aria-label={dismissLabel ?? l("Dismiss notification", "안내 닫기")}
      onClick={(event) => {
        const region = event.currentTarget.closest('[role="dialog"], .catalog-ui-target, .catalog-ui-curation');
        const next = Array.from(region?.querySelectorAll<HTMLButtonElement>('button:not([disabled])') ?? [])
          .find((button) => !button.closest('.curation-dismissible-notice') && button.getClientRects().length > 0);
        setDismissed(true);
        next?.focus();
      }}><X aria-hidden="true" /></Button>
  </div>;
}

export function CurationSourceNotices({ coverage, scopeId }: {
  coverage: readonly SourceCoverage[];
  scopeId: string;
}) {
  const { l } = useLocale();
  return <>{coverage.filter((row) => (row.status === "FAILED" || (row.status === "PARTIAL" && row.candidateCount === 0)) && !["CATALOG_API_DISABLED", "CATALOG_API_NOT_CONFIGURED", "CATALOG_API_RATE_LIMITED", "CATALOG_LOCAL_DAILY_LIMIT", "CATALOG_QUOTA_EXHAUSTED", "CATALOG_QUOTA_UNCONFIRMED"].includes(row.reasonCode ?? "") && !(row.source === "AMAZON" && isAmazonAdmissionUnavailable(row.reasonCode))).map((row) => {
    const source = sourceLabel(row.source, l);
    return <CurationDismissibleNotice key={row.source}
      noticeId={JSON.stringify([scopeId, row.source, row.status, row.reasonCode])}
      tone={row.status === "FAILED" ? "danger" : "warning"}
      dismissLabel={l("Dismiss {source} notification", "{source} 안내 닫기", { source })}>
      {l("{source} research was incomplete. Previously saved choices are preserved.", "{source} 조사가 일부 완료되지 않았습니다. 기존에 저장한 후보는 유지됩니다.", { source })}
    </CurationDismissibleNotice>;
  })}</>;
}
