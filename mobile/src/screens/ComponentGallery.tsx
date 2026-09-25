import { useMemo, useState } from "react";
import { ScrollView, StyleSheet, Text, View } from "react-native";

import { ActionButton } from "../components/ActionButton";
import { AgentActivitySheet } from "../components/AgentActivitySheet";
import { AgentAvatar } from "../components/AgentAvatar";
import { BackgroundResearchPanel } from "../components/BackgroundResearchPanel";
import { BlockingQuestionCard } from "../components/BlockingQuestionCard";
import {
  BrowserRunPanel,
  type BrowserControlMode,
  type BrowserRunPresentation,
} from "../components/BrowserRunPanel";
import { BrowserRunSheet } from "../components/BrowserRunSheet";
import { BudgetSheet, type BudgetPreviewView } from "../components/BudgetSheet";
import { CandidateCard, type CandidateCardView } from "../components/CandidateCard";
import {
  ClarificationSheet,
  type ClarificationDraft,
  type ClarificationQuestionView,
} from "../components/ClarificationSheet";
import { ChoiceCard } from "../components/ChoiceCard";
import { ExternalLinkApprovalSheet } from "../components/ExternalLinkApprovalSheet";
import { HumanHandoffSheet } from "../components/HumanHandoffSheet";
import { IntentComposer } from "../components/IntentComposer";
import { RunResultCard } from "../components/RunResultCard";
import { ShoppingGoalCard } from "../components/ShoppingGoalCard";
import {
  ShoppingMemorySheet,
  type ShoppingMemoryValue,
} from "../components/ShoppingMemorySheet";
import {
  ShoppingProposalCard,
  type ShoppingProposalStatus,
} from "../components/ShoppingProposalCard";
import { surfaceAssetRegistry, type BlockingQuestionSurface } from "../components/surfaceRegistry";
import { formatMoney } from "../domain";
import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, size, spacing, type } from "../theme/tokens";

export function ComponentGallery() {
  const { locale, t } = useLocale();
  const [composer, setComposer] = useState("");
  const [questionOpen, setQuestionOpen] = useState(false);
  const [budgetOpen, setBudgetOpen] = useState(false);
  const [activityOpen, setActivityOpen] = useState(false);
  const [memoryOpen, setMemoryOpen] = useState(false);
  const [externalApprovalOpen, setExternalApprovalOpen] = useState(false);
  const [browserOpen, setBrowserOpen] = useState(false);
  const [browserControl, setBrowserControl] = useState<BrowserControlMode>("agent");
  const [handoffOpen, setHandoffOpen] = useState(false);
  const [proposalStatus, setProposalStatus] = useState<ShoppingProposalStatus>("pending");
  const [monitorStatus, setMonitorStatus] = useState<"active" | "cancelled">("active");
  const [findingStatus, setFindingStatus] = useState<"new" | "hidden" | "added">("new");
  const [draft, setDraft] = useState<ClarificationDraft>({ customText: "", noPreference: false });
  const [budget, setBudget] = useState(80_000);
  const [memoryValue, setMemoryValue] = useState<ShoppingMemoryValue>({
    researchCountry: "KR",
    preferredCurrency: "KRW",
    uiLocale: "ko-KR",
  });
  const [memoryDraft, setMemoryDraft] = useState<ShoppingMemoryValue>(memoryValue);

  const question = useMemo<ClarificationQuestionView>(() => ({
    id: "gallery-question",
    title: t("gallery.questionTitle"),
    reason: t("gallery.questionReason"),
    optional: true,
    allowCustom: true,
    options: [
      {
        id: "include",
        title: t("gallery.includeTitle"),
        description: t("gallery.includeDescription"),
      },
      {
        id: "goods",
        title: t("gallery.goodsTitle"),
        description: t("gallery.goodsDescription"),
      },
    ],
  }), [t]);

  const sampleCandidate: CandidateCardView = {
    id: "gallery-candidate",
    kind: "service",
    title: t("gallery.candidateTitle"),
    source: t("gallery.candidateSource"),
    option: t("gallery.candidateOption"),
    price: { kind: "OBSERVED", amount: { currency: "KRW", amount: 42_000 } },
    quantity: 1,
    reason: t("gallery.candidateReason"),
    timing: t("gallery.candidateTiming"),
    selected: true,
    visualLabel: "C",
    visualTone: "iris",
    provenance: "Vitlane Catalog · product lookup",
  };

  const blockingQuestion: BlockingQuestionSurface = {
    kind: "question.choice",
    questionId: question.id,
    title: question.title,
    reason: question.reason,
    options: question.options.map((option) => ({ id: option.id, label: option.title })),
    allowCustom: question.allowCustom,
  };

  const preview: BudgetPreviewView = {
    knownTotal: { currency: "KRW", amount: 75_000 },
    remaining: { currency: "KRW", amount: Math.max(0, budget - 75_000) },
    overage: { currency: "KRW", amount: Math.max(0, 75_000 - budget) },
    totalState: "COMPLETE",
    comparisonState: "AVAILABLE",
    summary: t("gallery.budgetSummary"),
  };

  const browserRun: BrowserRunPresentation = {
    id: "gallery-browser-run",
    goal: t("gallery.browserGoal"),
    merchantName: t("gallery.browserMerchant"),
    origin: "shop.example",
    currentAction: browserControl === "agent"
      ? t("gallery.browserCurrentAction")
      : browserControl === "user"
        ? t("gallery.browserUserAction")
        : t("gallery.browserPausedAction"),
    stepLabel: t("gallery.browserStep"),
    controlMode: browserControl,
    state: browserControl === "agent" ? "preparing" : "needs-user",
    nativeOriginVerified: false,
  };

  return (
    <>
      <ScrollView contentContainerStyle={styles.page} keyboardShouldPersistTaps="handled">
        <View style={styles.content}>
          <Text style={styles.eyebrow}>{t("gallery.eyebrow")}</Text>
          <Text accessibilityRole="header" style={styles.title}>{t("gallery.title")}</Text>
          <Text style={styles.description}>{t("gallery.description")}</Text>

          <GallerySection title={t("gallery.sectionAgent")}>
            <View style={styles.agentPreview}>
              <AgentAvatar active size={72} />
              <View style={styles.agentPreviewCopy}>
                <Text style={styles.agentPreviewName}>{t("agent.name")}</Text>
                <Text style={styles.agentPreviewStatus}>{t("workspace.agentReadyStatus")}</Text>
              </View>
            </View>
          </GallerySection>

          <GallerySection title={t("gallery.sectionChoice")}>
            <StateLabel label={t("gallery.default")} />
            <ChoiceCard description={question.options[0]!.description} title={question.options[0]!.title} />
            <StateLabel label={t("gallery.selected")} />
            <ChoiceCard description={question.options[1]!.description} selected title={question.options[1]!.title} />
            <StateLabel label={t("gallery.disabled")} />
            <ChoiceCard description={question.options[0]!.description} disabled title={question.options[0]!.title} />
            <StateLabel label={t("gallery.long")} />
            <ChoiceCard
              description={t("gallery.longDescription")}
              title={t("gallery.longTitle")}
            />
            <ActionButton emphasis="secondary" label={t("gallery.openClarification")} onPress={() => setQuestionOpen(true)} />
            <StateLabel label={t("gallery.blockingQuestion")} />
            <BlockingQuestionCard
              onChoose={() => undefined}
              onOpenEditor={() => setQuestionOpen(true)}
              surface={blockingQuestion}
            />
          </GallerySection>

          <GallerySection title={t("gallery.sectionSurfaces")}>
            {Object.entries(surfaceAssetRegistry).map(([kind, definition]) => (
              <View key={kind} style={styles.registryRow}>
                <Text style={styles.registryKind}>{kind}</Text>
                <Text style={styles.registryComponent}>{definition.component} · {definition.contentModel}</Text>
              </View>
            ))}
          </GallerySection>

          <GallerySection title={t("gallery.sectionShopping")}>
            <View style={styles.reviewNotice}>
              <View style={styles.reviewDot} />
              <Text style={styles.reviewNoticeText}>{t("gallery.shoppingReviewNotice")}</Text>
            </View>

            <StateLabel label={surfaceAssetRegistry["goal.shopping"].component} />
            <ShoppingGoalCard
              goal={{
                id: "gallery-shopping-goal",
                title: t("gallery.shoppingGoalTitle"),
                summary: t("goal.summary", { targetCount: 2, candidateCount: 5, selectedCount: 1 }),
                state: "researching",
                progressDetail: t("goal.coveragePartial"),
              }}
              onOpenActivity={() => setActivityOpen(true)}
            />

            <StateLabel label={surfaceAssetRegistry["idea.proposal"].component} />
            <ShoppingProposalCard
              onAccept={() => setProposalStatus("accepted")}
              onDismiss={() => setProposalStatus("dismissed")}
              proposal={{
                id: "gallery-proposal",
                title: t("gallery.proposalTitle"),
                body: t("gallery.proposalBody"),
                reason: t("gallery.proposalReason"),
                responseMode: proposalStatus === "pending" ? "decision" : "none",
                status: proposalStatus,
              }}
            />

            <StateLabel label={surfaceAssetRegistry["monitor.research"].component} />
            <BackgroundResearchPanel
              findings={[{
                id: "gallery-finding",
                monitorId: "gallery-monitor",
                title: t("gallery.candidateTitle"),
                source: t("gallery.candidateSource"),
                priceLabel: formatMoney({ currency: "KRW", amount: 89_000 }, locale),
                reason: t("gallery.candidateReason"),
                status: findingStatus,
                openable: true,
              }]}
              monitors={[{
                id: "gallery-monitor",
                title: t("gallery.shoppingGoalTitle"),
                criteria: `${t("workspace.maximumPrice", {
                  amount: formatMoney({ currency: "KRW", amount: 120_000 }, locale),
                })} · ${t("memory.countryKR")}`,
                status: monitorStatus,
              }]}
              onAddFinding={() => setFindingStatus("added")}
              onCancelMonitor={() => setMonitorStatus("cancelled")}
              onHideFinding={() => setFindingStatus("hidden")}
              onOpenFinding={() => setExternalApprovalOpen(true)}
            />

            <StateLabel label={t("gallery.sheetPreviews")} />
            <View style={styles.previewActions}>
              <ActionButton
                compact
                emphasis="secondary"
                label={t("gallery.openActivity")}
                onPress={() => setActivityOpen(true)}
                style={styles.previewAction}
                testID="gallery.open-activity"
              />
              <ActionButton
                compact
                emphasis="secondary"
                label={t("gallery.openMemory")}
                onPress={() => {
                  setMemoryDraft(memoryValue);
                  setMemoryOpen(true);
                }}
                style={styles.previewAction}
                testID="gallery.open-memory"
              />
              <ActionButton
                compact
                emphasis="secondary"
                label={t("gallery.openExternalApproval")}
                onPress={() => setExternalApprovalOpen(true)}
                style={styles.previewAction}
                testID="gallery.open-external-approval"
              />
            </View>
            <ActionButton
              compact
              emphasis="quiet"
              label={t("gallery.resetShoppingPreview")}
              onPress={() => {
                setProposalStatus("pending");
                setMonitorStatus("active");
                setFindingStatus("new");
              }}
            />

            <StateLabel label={surfaceAssetRegistry["run.overview"].component} />
            <BrowserRunPanel
              onOpenActivity={() => setActivityOpen(true)}
              onResume={() => setBrowserControl("agent")}
              onStop={() => setBrowserControl("paused")}
              onTakeOver={() => setBrowserControl("user")}
              run={browserRun}
            />
            <View style={styles.previewActions}>
              <ActionButton
                compact
                emphasis="primary"
                label={t("gallery.openBrowser")}
                onPress={() => {
                  setBrowserControl("agent");
                  setBrowserOpen(true);
                }}
                style={styles.previewAction}
                testID="gallery.open-browser"
              />
              <ActionButton
                compact
                emphasis="secondary"
                label={t("gallery.openHandoff")}
                onPress={() => setHandoffOpen(true)}
                style={styles.previewAction}
                testID="gallery.open-handoff"
              />
            </View>
            <StateLabel label={surfaceAssetRegistry["result.run"].component} />
            <RunResultCard
              result={{
                id: "gallery-result",
                title: t("result.unknownTitle"),
                detail: t("gallery.resultDetail"),
                evidence: "unconfirmed",
                state: "result-unknown",
              }}
            />
          </GallerySection>

          <GallerySection title={t("gallery.sectionBudget")}>
            <View style={styles.inlineRow}>
              <Text style={styles.metric}>{t("gallery.budgetMetric")}</Text>
              <ActionButton compact emphasis="secondary" label={t("workspace.adjustBudget")} onPress={() => setBudgetOpen(true)} />
            </View>
          </GallerySection>

          <GallerySection title={t("gallery.sectionCandidate")}>
            <CandidateCard candidate={sampleCandidate} />
            <StateLabel label={t("gallery.rangePrice")} />
            <CandidateCard
              candidate={{
                ...sampleCandidate,
                id: "range",
                selected: false,
                price: {
                  kind: "RANGE",
                  minimum: { currency: "USD", amount: 24.99 },
                  maximum: { currency: "USD", amount: 39.99 },
                },
                source: "AMAZON",
              }}
            />
            <StateLabel label={t("gallery.unknownPrice")} />
            <CandidateCard
              candidate={{
                ...sampleCandidate,
                id: "unknown",
                selected: false,
                price: { kind: "UNKNOWN", reasonCode: "CATALOG_PRICE_UNOBSERVED" },
                source: "11ST",
              }}
            />
            <StateLabel label={t("gallery.loading")} />
            <CandidateCard candidate={{ ...sampleCandidate, id: "loading", selected: false, title: t("common.loading") }} loading />
            <StateLabel label={t("gallery.error")} />
            <View accessibilityRole="alert" style={styles.errorCard}>
              <Text style={styles.errorTitle}>{t("gallery.error")}</Text>
              <Text style={styles.errorBody}>{t("gallery.errorBody")}</Text>
              <ActionButton compact emphasis="quiet" label={t("common.retry")} onPress={() => undefined} />
            </View>
          </GallerySection>

          <GallerySection title={t("gallery.sectionComposer")}>
            <IntentComposer
              compact
              onChange={setComposer}
              onSubmit={() => undefined}
              placeholder={t("workspace.followUpPlaceholder")}
              submitLabel={t("workspace.sendFollowUp")}
              value={composer}
            />
          </GallerySection>
        </View>
      </ScrollView>

      <ClarificationSheet
        current={2}
        draft={draft}
        onApply={() => setQuestionOpen(false)}
        onBack={() => undefined}
        onClose={() => {
          setDraft({ customText: "", noPreference: false });
          setQuestionOpen(false);
        }}
        onDraftChange={setDraft}
        question={question}
        total={3}
        visible={questionOpen}
      />
      <BudgetSheet
        budgetOptions={[80_000]}
        currency="KRW"
        currentBudget={150_000}
        draftBudget={budget}
        onApply={() => setBudgetOpen(false)}
        onClose={() => setBudgetOpen(false)}
        onDraftChange={setBudget}
        preview={preview}
        visible={budgetOpen}
      />
      <AgentActivitySheet
        items={[
          {
            id: "gallery-activity-research",
            title: t("research.title"),
            detail: t("research.description"),
            state: "completed",
          },
          {
            id: "gallery-activity-compare",
            title: t("workspace.results"),
            detail: t("goal.coveragePartial"),
            state: "running",
          },
        ]}
        onClose={() => setActivityOpen(false)}
        status={t("workspace.agentWorkingStatus")}
        visible={activityOpen}
      />
      <ShoppingMemorySheet
        currentValue={memoryValue}
        draftValue={memoryDraft}
        onApply={() => {
          setMemoryValue(memoryDraft);
          setMemoryOpen(false);
        }}
        onClose={() => {
          setMemoryDraft(memoryValue);
          setMemoryOpen(false);
        }}
        onDraftChange={setMemoryDraft}
        visible={memoryOpen}
      />
      <ExternalLinkApprovalSheet
        onAllow={() => {
          setExternalApprovalOpen(false);
          setBrowserControl("agent");
          setBrowserOpen(true);
        }}
        onDeny={() => setExternalApprovalOpen(false)}
        target={externalApprovalOpen ? {
          destinationLabel: "review.example",
          priceLabel: formatMoney({ currency: "KRW", amount: 89_000 }, locale),
          productTitle: t("gallery.candidateTitle"),
          sellerName: t("gallery.candidateSource"),
        } : null}
        visible={externalApprovalOpen}
      />
      <BrowserRunSheet
        onClose={() => setBrowserOpen(false)}
        onOpenActivity={() => setActivityOpen(true)}
        onResume={() => setBrowserControl("agent")}
        onStop={() => setBrowserControl("paused")}
        onTakeOver={() => setBrowserControl("user")}
        reviewMode
        run={browserRun}
        visible={browserOpen}
      />
      <HumanHandoffSheet
        handoff={handoffOpen ? {
          reason: "final-action",
          merchantName: t("gallery.browserMerchant"),
          quoteSummary: t("gallery.handoffQuote"),
        } : null}
        onStop={() => setHandoffOpen(false)}
        onTakeOver={() => {
          setHandoffOpen(false);
          setBrowserControl("user");
          setBrowserOpen(true);
        }}
        visible={handoffOpen}
      />
    </>
  );
}

function GallerySection({ children, title }: { children: React.ReactNode; title: string }) {
  return (
    <View style={styles.section}>
      <Text accessibilityRole="header" style={styles.sectionTitle}>{title}</Text>
      {children}
    </View>
  );
}

function StateLabel({ label }: { label: string }) {
  return <Text style={styles.stateLabel}>{label}</Text>;
}

const styles = StyleSheet.create({
  page: { alignItems: "center", padding: spacing[4], paddingBottom: spacing[12] },
  content: { maxWidth: size.contentMax, width: "100%" },
  eyebrow: { color: colors.textAccent, fontSize: 12, fontWeight: "600", letterSpacing: 0.8, lineHeight: 18 },
  title: { color: colors.text, fontSize: type.hero, fontWeight: "600", lineHeight: type.heroLine, marginTop: spacing[2] },
  description: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine, marginTop: spacing[2] },
  section: {
    backgroundColor: colors.surface,
    borderColor: colors.border,
    borderRadius: radius.sheet,
    borderWidth: StyleSheet.hairlineWidth,
    gap: spacing[3],
    marginTop: spacing[6],
    padding: spacing[4],
  },
  sectionTitle: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  agentPreview: { alignItems: "center", flexDirection: "row", gap: spacing[4] },
  agentPreviewCopy: { flex: 1 },
  agentPreviewName: { color: colors.text, fontSize: 18, fontWeight: "600", lineHeight: 25 },
  agentPreviewStatus: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine, marginTop: spacing[1] },
  stateLabel: { color: colors.textMuted, fontSize: type.helper, fontWeight: "500", lineHeight: type.helperLine, marginTop: spacing[2] },
  inlineRow: { alignItems: "center", flexDirection: "row", flexWrap: "wrap", gap: spacing[3], justifyContent: "space-between" },
  metric: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  registryRow: { borderBottomColor: colors.border, borderBottomWidth: StyleSheet.hairlineWidth, gap: spacing[1], paddingVertical: spacing[2] },
  registryKind: { color: colors.textAccent, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  registryComponent: { color: colors.textMuted, fontSize: 12, lineHeight: 18 },
  errorCard: { backgroundColor: colors.dangerSoft, borderRadius: radius.product, gap: spacing[2], padding: spacing[4] },
  errorTitle: { color: colors.danger, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  errorBody: { color: colors.text, fontSize: type.helper, lineHeight: type.helperLine },
  reviewNotice: { alignItems: "flex-start", backgroundColor: colors.warningSoft, borderRadius: radius.overlay, flexDirection: "row", gap: spacing[2], padding: spacing[3] },
  reviewDot: { backgroundColor: colors.warning, borderRadius: radius.pill, height: 8, marginTop: 6, width: 8 },
  reviewNoticeText: { color: colors.text, flex: 1, fontSize: type.helper, lineHeight: type.helperLine },
  previewActions: { flexDirection: "row", flexWrap: "wrap", gap: spacing[2] },
  previewAction: { flexGrow: 1 },
});
