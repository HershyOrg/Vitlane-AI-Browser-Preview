import { useEffect, useState } from "react";
import {
  Navigate,
  type NavigateFunction,
  useNavigate,
  useParams,
} from "react-router";
import type { PlanResult } from "../shared/api/types";
import { messageOf, usePlanFlow } from "../products/curation/planning/app/usePlanFlow";
import { PlanCreator } from "../products/curation/planning/iface/PlanCreator";
import { getPlan } from "../products/curation/infra/curationApi";
import {
  ButtonLink,
  Notice,
  PageHeader,
  FeedbackState,
} from "../shared/ui";
import { useLocale } from "../shared/i18n";

function continueCreatedPlan(
  navigate: NavigateFunction,
  result: PlanResult,
) {
  // Intent creation only navigates. The curation workspace shows the agent
  // work panel, so the handoff waits for the user instead of switching apps
  // straight out of a form submit.
  navigate(`/curations/${result.curation.id}`);
}

export function HomePage() {
  const navigate = useNavigate();
  const flow = usePlanFlow();
  const { t } = useLocale();

  return (
    <div className="shell-home">
      {flow.error && (
        <Notice announce title={t("home.saveFailed")} tone="danger">
          {flow.error}
        </Notice>
      )}
      <PlanCreator
        initialForm={flow.initialForm}
        working={flow.working}
        onSubmit={async (form) => {
          const result = await flow.submitPlan(form);
          if (result) continueCreatedPlan(navigate, result);
        }}
      />
    </div>
  );
}

export function NewPlanPage() {
  const navigate = useNavigate();
  const flow = usePlanFlow();
  const { t } = useLocale();
  return (
    <div className="shell-page">
      {flow.error && (
        <Notice announce title={t("home.saveFailed")} tone="danger">
          {flow.error}
        </Notice>
      )}
      <PlanCreator
        initialForm={flow.initialForm}
        working={flow.working}
        onSubmit={async (form) => {
          const result = await flow.submitPlan(form);
          if (result) continueCreatedPlan(navigate, result);
        }}
      />
    </div>
  );
}

export function PlanResumePage() {
  const { t } = useLocale();
  const { planId = "" } = useParams();
  const [result, setResult] = useState<PlanResult | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    getPlan(planId)
      .then((loaded) => {
        if (active) setResult(loaded);
      })
      .catch((caught) => {
        if (active) setError(messageOf(caught));
      });
    return () => {
      active = false;
    };
  }, [planId]);

  if (error) return <ResourceError message={error} />;
  if (!result) {
    return (
      <FeedbackState
        description={t("resume.loading")}
        state="loading"
      />
    );
  }
  return <Navigate replace to={`/curations/${result.curation.id}`} />;
}

export function NotFoundPage() {
  const { t } = useLocale();
  return (
    <section className="shell-page catalog-ui-not-found">
      <PageHeader
        description={t("error.checkAddress")}
        eyebrow={t("error.invalidAddress")}
        title={t("error.notFound")}
      />
      <div className="shell-inline-actions">
        <ButtonLink emphasis="primary" href="/">
          {t("error.goHome")}
        </ButtonLink>
      </div>
    </section>
  );
}

function ResourceError({ message }: { message: string }) {
  const { t } = useLocale();
  return (
    <section className="shell-page">
      <PageHeader
        description={t("error.checkAddress")}
        eyebrow={t("error.needsAttention")}
        title={message}
      />
      <div className="shell-inline-actions">
        <ButtonLink emphasis="primary" href="/">
          {t("error.goHome")}
        </ButtonLink>
      </div>
    </section>
  );
}
