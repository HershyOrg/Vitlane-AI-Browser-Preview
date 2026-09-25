import { act, fireEvent, render, waitFor, within } from "@testing-library/react-native";
import { Linking, StyleSheet } from "react-native";

import type { WorkspaceView } from "../domain";
import { LocaleProvider } from "../i18n/LocaleProvider";
import { colors } from "../theme/tokens";
import { WorkspaceScreen } from "./WorkspaceScreen";

const localized = (value: string) => ({ "ko-KR": value, "en-US": value });

function workspaceWithStatus(
  status: NonNullable<WorkspaceView["processing"]>["status"],
): WorkspaceView {
  const zero = { currency: "KRW" as const, amount: 0 };
  return {
    id: "workspace-1",
    mode: "server",
    title: localized("준비안"),
    intent: "파티를 준비해줘",
    revision: 1,
    notice: localized("실제 서버 응답"),
    sourceLabel: localized("Vitlane"),
    conditions: [],
    questions: [],
    answers: [],
    activeQuestionId: null,
    plan: {
      id: "plan-1",
      revision: 1,
      items: [],
      shippingByMerchant: {},
      totals: {
        subtotal: zero,
        knownShipping: zero,
        knownTotal: zero,
        totalState: "COMPLETE",
        hasUnknownFees: false,
        unknownMerchantIds: [],
        budget: zero,
        remainingBudget: zero,
        overBudget: zero,
        budgetComparisonState: "AVAILABLE",
        budgetState: "UNLIMITED",
      },
    },
    budgetOptions: [],
    followUpSuggestions: [],
    candidates: [],
    activeCandidateIds: [],
    selectedCandidateIds: [],
    lastChange: [],
    processing: {
      status,
      label: "서버 작업",
      shouldPoll: false,
    },
  };
}

const noOpAsync = async () => null;

describe("WorkspaceScreen processing truthfulness", () => {
  afterEach(() => jest.restoreAllMocks());

  it.each([
    {
      status: "FAILED" as const,
      statusText: "요청을 마치지 못했어요",
      assistantText: "요청을 완료하지 못했어요. 상태를 확인한 뒤 다시 시도해 주세요.",
      color: colors.danger,
    },
    {
      status: "CANCELLED" as const,
      statusText: "요청이 취소됐어요",
      assistantText: "요청이 취소됐어요. 후속 수정으로 다시 요청할 수 있어요.",
      color: colors.warning,
    },
    {
      status: "WAITING_SELECTION" as const,
      statusText: "선택을 기다리고 있어요",
      assistantText: "서버가 선택이나 결과 확인을 기다리고 있어요. 현재 상태를 다시 확인할 수 있어요.",
      color: colors.warning,
    },
    {
      status: "RESULT_CONFIRMATION_REQUIRED" as const,
      statusText: "Vitlane 운영 확인이 필요해요",
      assistantText: "외부 작업 결과를 확정할 수 없어 조사를 안전하게 멈췄어요. 저장된 후보는 그대로 유지되며 Vitlane 운영 확인이 필요해요.",
      color: colors.warning,
    },
  ])("renders $status as a terminal or waiting state", async ({ status, statusText, assistantText, color }) => {
    const view = await render(
      <LocaleProvider>
        <WorkspaceScreen
          busy={false}
          error={null}
          onAnswerQuestion={noOpAsync}
          onApplyBudget={noOpAsync}
          onClearError={jest.fn()}
          onPreviewBudget={noOpAsync}
          onRefresh={noOpAsync}
          onSubmitFollowUp={noOpAsync}
          workspace={workspaceWithStatus(status)}
        />
      </LocaleProvider>,
    );

    expect(view.getByText(statusText)).toBeTruthy();
    expect(view.getByText(assistantText)).toBeTruthy();
    expect(StyleSheet.flatten(view.getByTestId("workspace.agent-status-dot").props.style)).toEqual(
      expect.objectContaining({ backgroundColor: color }),
    );
    expect(view.queryByText("요청을 처리했어요.")).toBeNull();
    if (status === "WAITING_SELECTION") {
      expect(view.getByRole("button", { name: "상태 새로고침" })).toBeTruthy();
    }
  });

  it("shows provider failure even when the intelligence operation succeeded", async () => {
    const workspace = workspaceWithStatus("SUCCEEDED");
    const view = await render(
      <LocaleProvider>
        <WorkspaceScreen
          busy={false}
          error={null}
          onAnswerQuestion={noOpAsync}
          onApplyBudget={noOpAsync}
          onClearError={jest.fn()}
          onPreviewBudget={noOpAsync}
          onRefresh={noOpAsync}
          onSubmitFollowUp={noOpAsync}
          workspace={{
            ...workspace,
            notice: localized("일부 소스는 불러오지 못했어요."),
            sourceCoverage: [{
              source: "SHOPIFY",
              status: "FAILED",
              candidateCount: 0,
              reasonCode: "UPSTREAM_FAILED",
            }],
          }}
        />
      </LocaleProvider>,
    );

    expect(view.getByText("일부 소스는 불러오지 못했어요.")).toBeTruthy();
    expect(StyleSheet.flatten(view.getByTestId("workspace.provider-notice").props.style)).toEqual(
      expect.objectContaining({ backgroundColor: colors.dangerSoft }),
    );
    expect(view.getByText(/현재 조건에 맞는 후보를 찾지 못했어요/)).toBeTruthy();
  });

  it("shows a committed overage instead of a positive zero-remainder pill", async () => {
    const workspace = workspaceWithStatus("SUCCEEDED");
    const view = await render(
      <LocaleProvider>
        <WorkspaceScreen
          busy={false}
          error={null}
          onAnswerQuestion={noOpAsync}
          onApplyBudget={noOpAsync}
          onClearError={jest.fn()}
          onPreviewBudget={noOpAsync}
          onRefresh={noOpAsync}
          onSubmitFollowUp={noOpAsync}
          workspace={{
            ...workspace,
            plan: {
              ...workspace.plan,
              totals: {
                ...workspace.plan.totals,
                knownTotal: { currency: "KRW", amount: 95_000 },
                budget: { currency: "KRW", amount: 80_000 },
                remainingBudget: { currency: "KRW", amount: 0 },
                overBudget: { currency: "KRW", amount: 15_000 },
                budgetState: "LIMITED",
              },
            },
          }}
        />
      </LocaleProvider>,
    );

    expect(view.getByText("예산 초과 15,000원")).toBeTruthy();
    expect(view.queryByText("예산 잔액 0원")).toBeNull();
  });

  it("labels a mixed-currency cart as a partial total and withholds budget comparison", async () => {
    const workspace = workspaceWithStatus("SUCCEEDED");
    const view = await render(
      <LocaleProvider>
        <WorkspaceScreen
          busy={false}
          error={null}
          onAnswerQuestion={noOpAsync}
          onApplyBudget={noOpAsync}
          onClearError={jest.fn()}
          onPreviewBudget={noOpAsync}
          onRefresh={noOpAsync}
          onSubmitFollowUp={noOpAsync}
          workspace={{
            ...workspace,
            plan: {
              ...workspace.plan,
              totals: {
                ...workspace.plan.totals,
                knownTotal: { currency: "USD", amount: 24.99 },
                totalState: "PARTIAL",
                budget: { currency: "KRW", amount: 80_000 },
                budgetComparisonState: "INCOMPLETE",
                budgetState: "LIMITED",
              },
            },
          }}
        />
      </LocaleProvider>,
    );

    expect(view.getByText("현재 확인된 부분 합계")).toBeTruthy();
    expect(view.getByText("USD 24.99")).toBeTruthy();
    expect(view.getByText(/예산 잔액을 계산할 수 없어요/)).toBeTruthy();
    expect(view.queryByText(/예산 잔액 0원/)).toBeNull();
  });

  it("previews an arbitrary positive budget against the live server", async () => {
    const onPreviewBudget = jest.fn(async () => null);
    const workspace = workspaceWithStatus("SUCCEEDED");
    const view = await render(
      <LocaleProvider>
        <WorkspaceScreen
          busy={false}
          error={null}
          onAnswerQuestion={noOpAsync}
          onApplyBudget={noOpAsync}
          onClearError={jest.fn()}
          onPreviewBudget={onPreviewBudget}
          onRefresh={noOpAsync}
          onSubmitFollowUp={noOpAsync}
          workspace={{
            ...workspace,
            plan: {
              ...workspace.plan,
              totals: {
                ...workspace.plan.totals,
                budget: { currency: "KRW", amount: 80_000 },
                remainingBudget: { currency: "KRW", amount: 80_000 },
                budgetState: "LIMITED",
              },
            },
          }}
        />
      </LocaleProvider>,
    );

    await act(async () => {
      fireEvent.press(view.getByLabelText("예산 조정"));
    });
    await act(async () => {
      fireEvent.changeText(view.getByTestId("budget.amount"), "123000");
    });

    await waitFor(() => expect(onPreviewBudget).toHaveBeenLastCalledWith(123000));
    expect(view.queryByText(/지원하지 않는 데모 예산/)).toBeNull();
  });

  it("reviews product, sign-in, checkout approval, and payment handoff in one fixture session", async () => {
    const openURL = jest.spyOn(Linking, "openURL").mockResolvedValue(undefined);
    const base = workspaceWithStatus("SUCCEEDED");
    const candidateId = "candidate-browser";
    const view = await render(
      <LocaleProvider>
        <WorkspaceScreen
          busy={false}
          error={null}
          onAnswerQuestion={noOpAsync}
          onApplyBudget={noOpAsync}
          onClearError={jest.fn()}
          onPreviewBudget={noOpAsync}
          onRefresh={noOpAsync}
          onSubmitFollowUp={noOpAsync}
          workspace={{
            ...base,
            mode: "fixture",
            activeCandidateIds: [candidateId],
            candidates: [{
              id: candidateId,
              title: localized("파란색 러닝화"),
              description: localized("옵션과 배송 조건 확인 대상"),
              badge: localized("추천"),
              price: { kind: "OBSERVED", amount: { currency: "KRW", amount: 89_000 } },
              itemIds: [],
              tone: "lavender",
              fitReasons: [localized("예산 안의 후보")],
              provenance: {
                source: "가상 판매처",
                productUrl: "https://shop.example.com/products/runner-blue",
                purchaseRoute: "EXTERNAL",
              },
            }],
          }}
        />
      </LocaleProvider>,
    );

    await act(async () => {
      fireEvent.press(view.getByRole("button", { name: "판매처에서 보기" }));
    });

    const run = await view.findByTestId("run.screen");
    expect(openURL).not.toHaveBeenCalled();
    expect(within(run).getByTestId("browser.origin-host").props.children).toBe("shop.example.com");
    expect(within(run).getByText("UI 검수용 화면 · 실제 브라우저 아님")).toBeTruthy();
    await waitFor(() => expect(within(run).getByTestId("browser.session.state.product")).toBeTruthy());

    await act(async () => {
      fireEvent.press(within(run).getByTestId("browser.prepare-purchase"));
    });
    expect(view.getByText("AI가 멈췄어요. 비밀번호와 인증 정보는 직접 입력해 주세요.")).toBeTruthy();
    await act(async () => {
      fireEvent.press(view.getByRole("button", { name: "직접 계속하기" }));
    });

    expect(within(run).getByTestId("browser.session.state.sign_in_required")).toBeTruthy();
    expect(within(run).getAllByText("직접 조작 중").length).toBeGreaterThan(0);
    await act(async () => {
      fireEvent.press(within(run).getByTestId("browser.sign-in-completed"));
    });
    expect(within(run).getByTestId("browser.session.state.signed_in")).toBeTruthy();
    expect(within(run).getByText("로그인 뒤 새 상태를 확인했어요")).toBeTruthy();
    expect(within(run).getByText("AI 조작 중")).toBeTruthy();

    await act(async () => {
      fireEvent.press(within(run).getByTestId("browser.prepare-checkout"));
    });
    expect(within(run).getByTestId("browser.session.state.checkout_ready")).toBeTruthy();
    expect(view.getByText("최종 결제 화면을 열까요?")).toBeTruthy();
    expect(view.getByText(/결제 버튼은 사용자가 직접 눌러야 합니다/)).toBeTruthy();
    await act(async () => {
      fireEvent.press(view.getByRole("button", { name: "최종 결제 화면 열기" }));
    });

    expect(within(run).getByTestId("browser.session.state.payment_handoff")).toBeTruthy();
    expect(within(run).getByText("최종 결제는 직접 완료해 주세요")).toBeTruthy();
    expect(within(run).getByText("판매처 결제 버튼 · 사용자만 누름")).toBeTruthy();
    expect(within(run).getAllByText("직접 조작 중").length).toBeGreaterThan(0);
    expect(within(run).getByTestId("browser.origin-host").props.children).toBe("shop.example.com");
  });

  it("mounts the native iOS merchant viewport for a server candidate without claiming AI control", async () => {
    const openURL = jest.spyOn(Linking, "openURL").mockResolvedValue(undefined);
    const base = workspaceWithStatus("SUCCEEDED");
    const candidateId = "candidate-native";
    const onStartBrowserRun = jest.fn(async () => ({
      id: "f1111111-1111-4111-8111-111111111111",
      curationId: base.id,
      candidateId,
      productUrl: "https://canonical.example.test/products/server-approved",
      merchantOrigin: "https://canonical.example.test",
      merchantHost: "canonical.example.test",
      state: "NAVIGATION_APPROVED" as const,
      controlOwner: "NONE" as const,
      version: 2,
    }));
    const view = await render(
      <LocaleProvider>
        <WorkspaceScreen
          busy={false}
          error={null}
          onAnswerQuestion={noOpAsync}
          onApplyBudget={noOpAsync}
          onClearError={jest.fn()}
          onPreviewBudget={noOpAsync}
          onRefresh={noOpAsync}
          onStartBrowserRun={onStartBrowserRun}
          onSubmitFollowUp={noOpAsync}
          workspace={{
            ...base,
            activeCandidateIds: [candidateId],
            candidates: [{
              id: candidateId,
              title: localized("파란색 러닝화"),
              description: localized("옵션과 배송 조건 확인 대상"),
              badge: localized("추천"),
              price: { kind: "OBSERVED", amount: { currency: "KRW", amount: 89_000 } },
              itemIds: [],
              tone: "lavender",
              fitReasons: [localized("예산 안의 후보")],
              provenance: {
                source: "가상 판매처",
                productUrl: "https://shop.example.com/products/runner-blue",
                purchaseRoute: "EXTERNAL",
              },
            }],
          }}
        />
      </LocaleProvider>,
    );

    await act(async () => {
      fireEvent.press(view.getByRole("button", { name: "판매처에서 보기" }));
    });
    const run = await view.findByTestId("run.screen");
    const viewport = within(run).getByTestId("browser.native-viewport");
    expect(onStartBrowserRun).toHaveBeenCalledWith(candidateId);
    expect(viewport.props.url).toBe("https://canonical.example.test/products/server-approved");
    expect(within(run).getByTestId("browser.origin-host").props.children).toBe("canonical.example.test");
    expect(within(run).getByText("AI 정지됨")).toBeTruthy();
    expect(within(run).getByText(/서버 Device Channel이 없어/)).toBeTruthy();
    expect(within(run).queryByText("아이디 또는 이메일")).toBeNull();
    expect(openURL).not.toHaveBeenCalled();
  });

  it("opens an approved Naver reservation search in the user-controlled native browser", async () => {
    const openURL = jest.spyOn(Linking, "openURL").mockResolvedValue(undefined);
    const onStartBrowserRun = jest.fn(async () => null);
    const base = workspaceWithStatus("SUCCEEDED");
    const view = await render(
      <LocaleProvider>
        <WorkspaceScreen
          busy={false}
          error={null}
          onAnswerQuestion={noOpAsync}
          onApplyBudget={noOpAsync}
          onClearError={jest.fn()}
          onPreviewBudget={noOpAsync}
          onRefresh={noOpAsync}
          onStartBrowserRun={onStartBrowserRun}
          onSubmitFollowUp={noOpAsync}
          workspace={{
            ...base,
            title: localized("성수 미용실"),
            goal: {
              title: localized("성수 미용실"),
              status: "ready",
              activeTargetCount: 1,
              candidateCount: 0,
              selectedCount: 0,
              unresolvedQuestionCount: 0,
              coverage: "NONE",
            },
          }}
        />
      </LocaleProvider>,
    );

    await act(async () => {
      fireEvent.press(view.getByTestId("browser-destination.NAVER_BOOKING"));
    });
    expect(view.getByText("이 지도에서 예약처를 직접 찾을까요?")).toBeTruthy();
    expect(view.getAllByText("map.naver.com").length).toBeGreaterThan(0);

    await act(async () => {
      fireEvent.press(view.getByRole("button", { name: "브라우저 열기" }));
    });

    const run = await view.findByTestId("run.screen");
    const viewport = within(run).getByTestId("browser.native-viewport");
    const url = new URL(viewport.props.url);
    expect(url.hostname).toBe("map.naver.com");
    expect(decodeURIComponent(url.pathname)).toContain("성수 미용실 예약");
    expect(onStartBrowserRun).not.toHaveBeenCalled();
    expect(within(run).getByText(/AI 제어는 시작되지 않습니다/)).toBeTruthy();
    expect(within(run).getAllByText("직접 조작 중").length).toBeGreaterThan(0);
    expect(openURL).not.toHaveBeenCalled();
  });

  it("omits default metadata, empty totals, and empty research from the conversation", async () => {
    const base = workspaceWithStatus("SUCCEEDED");
    const view = await render(
      <LocaleProvider>
        <WorkspaceScreen
          busy={false}
          error={null}
          onAnswerQuestion={noOpAsync}
          onApplyBudget={noOpAsync}
          onClearError={jest.fn()}
          onPreviewBudget={noOpAsync}
          onRefresh={noOpAsync}
          onSubmitFollowUp={noOpAsync}
          workspace={{
            ...base,
            title: localized("비피더스"),
            intent: "비피더스",
            conditions: [
              {
                id: "location",
                label: localized("조사 지역"),
                value: localized("Seoul, KR"),
                confirmed: false,
                reason: localized("앱에 설정된 조사 지역을 사용했어요."),
              },
              {
                id: "budget",
                label: localized("예산"),
                value: localized("제한 없음"),
                confirmed: false,
              },
              {
                id: "target:1",
                label: localized("찾는 항목"),
                value: localized("비피더스"),
                confirmed: false,
              },
            ],
            backgroundResearch: { availability: "disabled", subscriptions: [], findings: [] },
          }}
        />
      </LocaleProvider>,
    );
    expect(view.queryByText("준비 결과")).toBeNull();
    expect(view.queryByRole("header", { name: "먼저 이렇게 가정했어요" })).toBeNull();
    expect(view.queryByText("현재 조건")).toBeNull();
    expect(view.queryByText("조사 지역")).toBeNull();
    expect(view.queryByText("Seoul, KR")).toBeNull();
    expect(view.queryByText("준비안 요약")).toBeNull();
    expect(view.queryByText("선택 항목 0개")).toBeNull();
    expect(view.queryByText("0원")).toBeNull();
    expect(view.queryByLabelText("예산 조정")).toBeNull();
    expect(view.queryByText("현재 후보 0개")).toBeNull();
    expect(view.queryByRole("button", { name: "후보 접기" })).toBeNull();
    expect(view.queryByRole("header", { name: "계속 지켜보는 조건" })).toBeNull();
    expect(view.queryByText("이 서버에서는 조건 기반 딜 확인이 아직 활성화되지 않았어요.")).toBeNull();
    expect(view.getByRole("header", { name: "추천 준비안" })).toBeTruthy();
    expect(view.getByText(/현재 조건에 맞는 후보를 찾지 못했어요/)).toBeTruthy();
  });

  it("routes an approved research finding into the same browser sheet", async () => {
    const openURL = jest.spyOn(Linking, "openURL").mockResolvedValue(undefined);
    const base = workspaceWithStatus("SUCCEEDED");
    const view = await render(
      <LocaleProvider>
        <WorkspaceScreen
          busy={false}
          error={null}
          onAnswerQuestion={noOpAsync}
          onApplyBudget={noOpAsync}
          onClearError={jest.fn()}
          onPreviewBudget={noOpAsync}
          onRefresh={noOpAsync}
          onSubmitFollowUp={noOpAsync}
          workspace={{
            ...base,
            backgroundResearch: {
              availability: "available",
              subscriptions: [],
              findings: [{
                id: "finding-1",
                subscriptionId: "subscription-1",
                targetId: "target-1",
                status: "NEW",
                title: "새 러닝화",
                productUrl: "https://research.example.com/products/new-runner",
                price: { kind: "OBSERVED", amount: { currency: "KRW", amount: 72_000 } },
                provider: "리서치 판매처",
                reason: "가격이 내려갔어요",
                observedAt: "2026-09-24T01:00:00.000Z",
                createdAt: "2026-09-24T01:00:00.000Z",
              }],
            },
          }}
        />
      </LocaleProvider>,
    );

    await act(async () => {
      fireEvent.press(view.getByRole("button", { name: "판매처 확인" }));
    });
    await act(async () => {
      fireEvent.press(await view.findByRole("button", { name: "판매처 열기" }));
    });
    const run = await view.findByTestId("run.screen");
    expect(within(run).getByTestId("browser.origin-host").props.children).toBe("research.example.com");
    expect(within(run).queryByTestId("browser.native-viewport")).toBeNull();
    expect(await within(run).findByTestId("browser.session.state.product")).toBeTruthy();
    expect(openURL).not.toHaveBeenCalled();
  });
});
