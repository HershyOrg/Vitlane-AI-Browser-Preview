// @vitest-environment jsdom

import { readFileSync } from "node:fs";
import path from "node:path";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  CurationActionDescriptor,
  CurationWorkspaceModel,
  FollowUpMessage,
} from "../domain/types";
import type { ConversationMessage } from "../domain/conversation";
import { CurationComposerDock } from "./CurationComposerDock";
import { CurationWorkspace } from "./CurationWorkspace";

const curationWorkspaceCSS = readFileSync(
  path.resolve(
    process.cwd(),
    "src/products/curation/iface/curation-workspace.css",
  ),
  "utf8",
);

describe("CurationWorkspace", () => {
  let container: HTMLDivElement | undefined;

  it("suppresses a pending label only when progress is displayed for the same server action", () => {
    const active = { ...workspace, activeWork: { workTargetId: "round", label: "Research", status: "RUNNING" as const } };
    const last = [...active.timeline].reverse().find((entry) => entry.kind === "ACTION" && entry.action?.effectKind === "INTELLIGENCE");
    if (!last || last.kind !== "ACTION" || !last.action) throw new Error("Missing test action");
    const matching = renderToStaticMarkup(<CurationWorkspace workspace={active} displayedActivityActionId={last.action.id} />);
    const unrelated = renderToStaticMarkup(<CurationWorkspace workspace={active} displayedActivityActionId="other-action" />);
    expect(matching).not.toContain("Vitlane 응답 대기 중");
    expect(unrelated).toContain("Vitlane 응답 대기 중");
    expect(matching).toContain(last.command.split(": ").at(-1)!);
  });

  afterEach(() => {
    container?.remove();
    container = undefined;
    vi.clearAllMocks();
    vi.unstubAllGlobals();
  });

  it("composer를 workspace-owned sticky host로 옮기고 runtime 좌표를 만들지 않는다", async () => {
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(
        <CurationWorkspace
          workspace={curatingWorkspace()}
          renderArtifact={() => (
            <div data-testid="candidate-artifact">
              <CurationComposerDock>
                <button data-testid="context-composer" type="button">Composer</button>
              </CurationComposerDock>
            </div>
          )}
        />,
      );
    });

    const artifact = container.querySelector('[data-testid="candidate-artifact"]')!;
    const main = container.querySelector(".curation-workspace__main")!;
    const shell = container.querySelector(".curation-workspace__dock-shell")!;
    const host = shell.querySelector(".curation-composer-anchor")!;
    const dock = host.querySelector(".catalog-ui-composer-dock")! as HTMLElement;
    expect(dock).not.toBeNull();
    expect(host.contains(dock)).toBe(true);
    expect(artifact.contains(dock)).toBe(false);
    expect(follows(main, shell)).toBe(true);
    expect(dock.dataset.fixed).toBeUndefined();
    expect(dock.style.left).toBe("");
    expect(dock.style.width).toBe("");
    expect(dock.style.bottom).toBe("");
    expect(dock.style.getPropertyValue("--composer-left")).toBe("");
    expect(dock.style.getPropertyValue("--composer-width")).toBe("");
    expect(dock.style.getPropertyValue("--composer-bottom")).toBe("");

    await act(async () => root.unmount());
  });

  it("keeps result messages above the artifact and pending proposals below until answered", async () => {
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const result: FollowUpMessage = {
      id: "research-result", responseId: "response-1", kind: "RESULT",
      status: "ACKNOWLEDGED", version: 1, createdAt: "2026-09-12T00:00:00Z",
      content: { code: "RESEARCH_COMPLETED", added: 9 },
    };
    const proposal: FollowUpMessage = {
      ...result, id: "follow-up-proposal", kind: "PROPOSAL", status: "PENDING",
      content: { code: "LOW_AXIS_FIT", body: "더 적합한 후보를 찾아볼까요?" },
    };
    const render = (status: FollowUpMessage["status"]) => root.render(
      <CurationWorkspace
        workspace={{ ...curatingWorkspace(), conversation: {
          schemaVersion: "vitlane.curation-conversation.v1", version: 1,
          unfinished: false, requests: [], messages: [result, { ...proposal, status }],
        } }}
        onFollowUpResponse={() => {}}
        renderArtifact={(_artifact, context) => <>
          <div data-testid="candidate-artifact" />
          {context.conversationTail}
          <div data-testid="composer" />
        </>}
      />,
    );
    await act(async () => render("PENDING"));
    const artifact = container.querySelector('[data-testid="candidate-artifact"]')!;
    const resultNode = container.querySelector('[data-message-id="research-result"]')!;
    const pending = container.querySelector('[data-message-id="follow-up-proposal"]')!;
    expect(follows(resultNode, artifact)).toBe(true);
    expect(follows(artifact, pending)).toBe(true);
    expect(follows(pending, container.querySelector('[data-testid="composer"]')!)).toBe(true);
    expect(resultNode.querySelector("p")?.textContent).toBe("현재 기준으로 새 후보 9개를 찾았어요.");
    expect(resultNode.querySelector("button")).toBeNull();
    expect(pending.querySelectorAll("button")).toHaveLength(2);

    await act(async () => render("DISMISSED"));
    const history = container.querySelectorAll('[data-message-id="follow-up-proposal"]');
    expect(history).toHaveLength(1);
    expect(follows(history[0], container.querySelector('[data-testid="candidate-artifact"]')!)).toBe(true);
    expect(history[0].querySelector(".curation-conversation-bubble.is-vitlane p")?.textContent).toBe(proposal.content.body);
    expect(history[0].querySelector("button")).toBeNull();
    await act(async () => root.unmount());
  });

  it("페이지형 제목 없이 ruled transcript, 과거 compact diff와 최신 live artifact를 구분한다", () => {
    const html = renderToStaticMarkup(
      <CurationWorkspace
        workspace={workspace}
        renderArtifact={(artifact) => (
          <button type="button">{artifact.title} 후보 비교</button>
        )}
      />,
    );

    expect(html).not.toContain('aria-label="큐레이션 목록"');
    expect(html).not.toContain("curation-rail");
    expect(html).not.toContain("curation-workspace__header");
    expect(html).not.toContain("구매 추적");
    expect(html).not.toContain("일부 구매 기록");
    expect(html).not.toContain(
      "@TargetList:curation-1-AddTarget: 업무용 조명도 추가",
    );
    expect(html).toContain('data-conversation-presentation="result"');
    expect(html).toContain("첫 Target 목록을 만들었습니다.");
    expect(html).not.toContain("이전 결과");
    expect(html).not.toContain("<dt>추가</dt>");
    expect(html).toContain('data-live-artifact="true"');
    expect(html).toContain("업무 환경 큐레이션 후보 비교");
    expect(html).not.toContain("AVAILABLE ACTIONS");
    expect(html).not.toContain("catalog-ui-composer-dock");
    expect(html).not.toContain('role="combobox"');
    expect(html).toContain("curation-user-bubble");
    expect(html).toContain("업무용 조명도 추가");
    expect(html).not.toContain("ResearchWorkspace");
  });

  it("CURATING에서도 최초 요청은 우측 사용자 말풍선으로 한 번만 표시한다", () => {
    const originalIntent = "고급 단색(검정 잉크) 볼펜 찾아줘";
    const html = renderToStaticMarkup(
      <CurationWorkspace
        workspace={{
          ...workspace,
          curation: {
            ...workspace.curation,
            title: originalIntent,
            phase: "CURATING",
          },
          timeline: [
            {
              id: "intent-action",
              kind: "ACTION",
              createdAt: "2026-07-31T01:00:00Z",
              command: `@Intent-NextStep: ${originalIntent}`,
            },
            workspace.timeline.at(-1)!,
          ],
        }}
      />,
    );

    expect(html.match(new RegExp(originalIntent.replace(/[()]/g, "\\$&"), "g")))
      .toHaveLength(1);
    expect(html).toContain("curation-user-bubble");
    expect(html).not.toContain("큐레이팅 · v");
  });

  it("CURATING 완료 결과를 간결한 Vitlane bubble로 현재 artifact 위에 보존한다", () => {
    const html = renderToStaticMarkup(
      <CurationWorkspace workspace={curatingWorkspace()} />,
    );
    const previousResult = html.indexOf('data-conversation-presentation="result"');
    const currentArtifact = html.indexOf('data-live-artifact="true"');

    expect(previousResult).toBeGreaterThanOrEqual(0);
    expect(previousResult).toBeLessThan(currentArtifact);
    expect(html).toContain("첫 Target 목록을 만들었습니다.");
    expect(html).not.toContain("curation-compact-artifact");
    expect(html).not.toContain("<dt>추가</dt>");
  });

  it("Vitlane 답변은 좌측 accent 없이 일반 bubble border를 사용한다", () => {
    const baseBubble = curationWorkspaceCSS.match(
      /\.curation-conversation-bubble\s*\{(?<rules>[^}]*)\}/,
    )?.groups?.rules;
    const vitlaneBubble = curationWorkspaceCSS.match(
      /\.curation-conversation-bubble\.is-vitlane\s*\{(?<rules>[^}]*)\}/,
    )?.groups?.rules;

    expect(baseBubble).toContain("var(--vt-semantic-color-border-default)");
    expect(vitlaneBubble).toBeDefined();
    expect(vitlaneBubble).not.toMatch(/border-(?:left|inline-start)(?:-(?:color|width))?\s*:/);
  });

  it("큐레이션 최초 진입은 animation 없이 scroll container의 최하단을 즉시 보여준다", async () => {
    container = document.createElement("div");
    container.className = "shell-product-body";
    Object.defineProperty(container, "scrollHeight", { value: 1200 });
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<CurationWorkspace workspace={workspace} />);
    });

    expect(container.scrollTop).toBe(1200);
    expect(container.querySelector(".curation-workspace__initial-tail")).not.toBeNull();
    await act(async () => root.unmount());
  });

  it("Target 확정으로 본문 높이가 늦게 늘어도 tail을 보던 사용자는 새 최하단을 계속 본다", async () => {
    // The workspace observes size for more than one reason (following the tail, the composer's shade): run them all.
    const resizeCallbacks: ResizeObserverCallback[] = [];
    class TestResizeObserver {
      constructor(callback: ResizeObserverCallback) {
        resizeCallbacks.push(callback);
      }
      observe() {}
      unobserve() {}
      disconnect() {}
    }
    vi.stubGlobal("ResizeObserver", TestResizeObserver);
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
      callback(0);
      return 1;
    });
    vi.stubGlobal("cancelAnimationFrame", () => {});

    container = document.createElement("div");
    container.className = "shell-product-body";
    let scrollHeight = 800;
    Object.defineProperty(container, "scrollHeight", {
      configurable: true,
      get: () => scrollHeight,
    });
    Object.defineProperty(container, "clientHeight", { value: 400 });
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<CurationWorkspace workspace={workspace} />));
    expect(container.scrollTop).toBe(800);

    scrollHeight = 1_400;
    await act(async () => {
      for (const callback of resizeCallbacks) callback([], {} as ResizeObserver);
    });
    expect(container.scrollTop).toBe(1_400);
    expect(container.textContent).not.toContain("새 응답 보기");

    await act(async () => root.unmount());
  });

  it("tail 근처에서는 새 사건을 따르고 과거 기록을 읽는 중에는 위치를 보존하며 새 응답을 안내한다", async () => {
    container = document.createElement("div");
    container.className = "shell-product-body";
    let scrollHeight = 1_000;
    Object.defineProperty(container, "scrollHeight", {
      configurable: true,
      get: () => scrollHeight,
    });
    Object.defineProperty(container, "clientHeight", { value: 400 });
    document.body.append(container);
    const root = createRoot(container);

    const render = (messages: ConversationMessage[]) => (
      <CurationWorkspace
        workspace={curatingWorkspace()}
        conversationMessages={messages}
        renderArtifact={(_artifact, context) => (
          <div>{context.conversationTail}</div>
        )}
      />
    );
    const firstMessage: ConversationMessage = {
      id: "follow-1",
      role: "USER",
      title: "나",
      body: "첫 요청",
      createdAt: "2026-07-31T03:00:00Z",
      state: "ACTIVE",
    };
    const secondMessage: ConversationMessage = {
      id: "follow-2",
      role: "VITLANE",
      title: "완료",
      body: "최신 응답",
      createdAt: "2026-07-31T03:01:00Z",
      state: "ACTIVE",
    };

    await act(async () => root.render(render([])));
    expect(container.scrollTop).toBe(1_000);

    container.scrollTop = 200;
    container.dispatchEvent(new Event("scroll"));
    scrollHeight = 1_400;
    await act(async () => root.render(render([firstMessage])));
    expect(container.scrollTop).toBe(200);
    expect(container.textContent).toContain("새 응답 보기");

    container.scrollTop = 1_000;
    container.dispatchEvent(new Event("scroll"));
    scrollHeight = 1_600;
    await act(async () => root.render(render([firstMessage, secondMessage])));
    expect(container.scrollTop).toBe(1_600);

    await act(async () => root.unmount());
  });

  it("외부효과 진행 중에는 user bubble을 live tail에 두고 범용 composer를 만들지 않는다", () => {
    const busyWorkspace: CurationWorkspaceModel = {
      ...workspace,
      activeWork: {
        workTargetId: "round-running",
        label: "후보 조사",
        status: "RUNNING",
      },
    };
    const html = renderToStaticMarkup(
      <CurationWorkspace workspace={busyWorkspace} />,
    );
    const artifactIndex = html.indexOf('data-live-artifact="true"');
    const pendingBubbleIndex = html.lastIndexOf("curation-user-bubble");

    expect(html).not.toContain("curation-active-work");
    expect(html).toContain("Vitlane 응답 대기 중");
    expect(artifactIndex).toBeGreaterThanOrEqual(0);
    expect(pendingBubbleIndex).toBeGreaterThan(artifactIndex);
    expect(html).toContain('aria-busy="true"');
    expect(html).not.toContain("catalog-ui-composer-dock");
  });

  it("전송 직후 optimistic 요청을 live artifact 아래 user bubble로 둔다", () => {
    const command =
      "@TargetList:curation-1-AddTarget: 모니터 암 추가";
    const html = renderToStaticMarkup(
      <CurationWorkspace
        workspace={workspace}
        working
        pendingTranscriptAction={{
          id: "pending-action",
          command,
          createdAt: "2026-07-31T04:01:00Z",
        }}
      />,
    );

    const artifactIndex = html.indexOf('data-live-artifact="true"');
    const pendingBubbleIndex = html.lastIndexOf("curation-user-bubble");
    expect(html).toContain("Vitlane 응답 대기 중");
    expect(html).toContain("모니터 암 추가");
    expect(html).not.toContain(command);
    expect(pendingBubbleIndex).toBeGreaterThan(artifactIndex);
    expect(html).not.toContain("catalog-ui-composer-dock");
  });

  it("CURATING 전송 직후 live user bubble을 artifact와 composer 사이에 한 번만 둔다", async () => {
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const busyWorkspace = curatingWorkspace({ active: true });

    await act(async () => {
      root.render(
        <CurationWorkspace
          workspace={busyWorkspace}
          renderArtifact={(_artifact, context) => (
            <div>
              <div data-testid="candidate-artifact">Candidate artifact</div>
              <div data-testid="live-tail">{context.conversationTail}</div>
              <div data-testid="context-composer">Composer</div>
            </div>
          )}
        />,
      );
    });

    const artifact = container.querySelector('[data-testid="candidate-artifact"]')!;
    const liveBubble = container.querySelector('[data-pending-conversation="true"]')!;
    const composer = container.querySelector('[data-testid="context-composer"]')!;
    expect(container.querySelectorAll('[data-pending-conversation="true"]')).toHaveLength(1);
    expect(follows(artifact, liveBubble)).toBe(true);
    expect(follows(liveBubble, composer)).toBe(true);

    await act(async () => root.unmount());
  });

  it("Auto optimistic user bubble을 durable Server Action과 중복하지 않는다", async () => {
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const busyWorkspace = curatingWorkspace({ active: true });
    const messages: ConversationMessage[] = [
      {
        id: "auto-user",
        role: "USER",
        title: "나",
        body: "업무용 의자를 다시 조사해줘",
        createdAt: "2026-07-31T04:01:00Z",
        state: "ACTIVE",
        turnId: "auto-turn",
        reconcilesServerAction: true,
      },
      {
        id: "auto-progress",
        role: "VITLANE",
        title: "Auto 요청을 접수했습니다",
        body: "요청하신 내용으로 다시 조사합니다.",
        createdAt: "2026-07-31T04:01:01Z",
        state: "ACTIVE",
        turnId: "auto-turn",
      },
    ];

    await act(async () => {
      root.render(
        <CurationWorkspace
          workspace={busyWorkspace}
          conversationMessages={messages}
          renderArtifact={(_artifact, context) => (
            <div data-testid="live-tail">{context.conversationTail}</div>
          )}
        />,
      );
    });

    const liveTail = container.querySelector('[data-testid="live-tail"]')!;
    const serverUser = liveTail.querySelector(".curation-user-bubble")!;
    const progress = liveTail.querySelector('[data-conversation-message-id="auto-progress"]')!;
    expect(liveTail.querySelectorAll(".curation-user-bubble")).toHaveLength(1);
    expect(liveTail.querySelector('[data-conversation-message-id="auto-user"]')).toBeNull();
    expect(follows(serverUser, progress)).toBe(true);

    await act(async () => root.unmount());
  });

  it("완료 갱신 시 user와 Vitlane Diff를 모두 artifact 위로 보내고 live tail을 비운다", async () => {
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const messages: ConversationMessage[] = [
      {
        id: "message-user",
        role: "USER",
        title: "나",
        body: "Target 재조사 · 가격대를 조정해줘",
        createdAt: "2026-07-31T03:00:00Z",
        state: "SETTLED",
      },
      {
        id: "message-started",
        role: "VITLANE",
        title: "재조사를 시작했습니다",
        body: "기존 후보를 유지하며 조사합니다.",
        createdAt: "2026-07-31T03:01:00Z",
        state: "SETTLED",
      },
      {
        id: "message-complete",
        role: "VITLANE",
        title: "조사 완료",
        body: "Candidate를 갱신했습니다.",
        createdAt: "2026-07-31T03:02:00Z",
        state: "SETTLED",
        diff: { changed: ["Commuter backpack"] },
      },
    ];

    await act(async () => {
      root.render(
        <CurationWorkspace
          workspace={curatingWorkspace()}
          conversationMessages={messages}
          renderArtifact={(_artifact, context) => (
            <div>
              <div data-testid="candidate-artifact">Candidate artifact</div>
              <div data-testid="live-tail">{context.conversationTail}</div>
              <div data-testid="context-composer">Composer</div>
            </div>
          )}
        />,
      );
    });

    const artifact = container.querySelector('[data-testid="candidate-artifact"]')!;
    const composer = container.querySelector('[data-testid="context-composer"]')!;
    const historicalUser = container.querySelector('[data-conversation-message-id="message-user"]')!;
    const historicalStarted = container.querySelector('[data-conversation-message-id="message-started"]')!;
    const latest = container.querySelector('[data-conversation-message-id="message-complete"]')!;
    expect(follows(historicalUser, artifact)).toBe(true);
    expect(follows(historicalStarted, artifact)).toBe(true);
    expect(follows(latest, artifact)).toBe(true);
    expect(follows(artifact, composer)).toBe(true);
    expect(latest.getAttribute("data-conversation-presentation")).toBe("diff");
    expect(latest.classList).toContain("is-vitlane");
    expect(latest.classList).not.toContain("is-diff");
    expect(latest.querySelector("strong")?.textContent).toBe("Vitlane");
    expect(latest.querySelector("dl")).toBeNull();
    expect(latest.textContent).toContain("Candidate를 갱신했습니다.");
    expect(container.querySelector('[data-testid="live-tail"]')
      ?.querySelectorAll(".curation-conversation-bubble")).toHaveLength(0);

    await act(async () => root.unmount());
  });

  it("empty, loading, error 상태가 각각 다음 행동을 설명한다", () => {
    const empty = renderToStaticMarkup(<CurationWorkspace />);
    const loading = renderToStaticMarkup(
      <CurationWorkspace loading />,
    );
    const failed = renderToStaticMarkup(
      <CurationWorkspace error="네트워크 연결을 확인해 주세요." />,
    );

    expect(empty).toContain("새 큐레이션을 시작하세요.");
    expect(loading).toContain("큐레이션 흐름을 불러오는 중");
    expect(loading).toContain('aria-busy="true"');
    expect(failed).toContain("작업 흐름을 불러오지 못했습니다.");
    expect(failed).toContain("네트워크 연결을 확인해 주세요.");
  });
});

function follows(first: Node, second: Node) {
  return Boolean(
    first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING,
  );
}

function curatingWorkspace({ active = false }: { active?: boolean } = {}) {
  return {
    ...workspace,
    curation: {
      ...workspace.curation,
      phase: "CURATING" as const,
    },
    activeWork: active
      ? {
          workTargetId: "round-running",
          label: "후보 조사",
          status: "RUNNING" as const,
        }
      : undefined,
  };
}

const workspace: CurationWorkspaceModel = {
  curation: {
    id: "curation-1",
    title: "업무 환경 큐레이션",
    phase: "PLANNING",
    version: 4,
    coverage: "PARTIAL",
    updatedAt: "2026-07-31T04:00:00Z",
  },
  curations: [
    {
      id: "curation-1",
      title: "업무 환경 큐레이션",
      phase: "PLANNING",
      coverage: "PARTIAL",
      updatedAt: "2026-07-31T04:00:00Z",
    },
    {
      id: "curation-2",
      title: "여름 여행 준비",
      phase: "CURATING",
      coverage: "NONE",
      updatedAt: "2026-07-30T04:00:00Z",
      activeWork: true,
    },
  ],
  availableActions: [
    actionDescriptor({
      id: "PLANNING_ADD_TARGETS",
      alias: "@TargetList-AddTarget",
      bodySchema: "TEXT_REQUIRED",
      subjectType: "TARGET_LIST",
      subjectIDRequired: true,
    }),
    actionDescriptor({
      id: "PLANNING_START_CURATING",
      alias: "@Planning-NextStep",
      bodySchema: "NONE",
      subjectType: "CURATION",
      subjectIDRequired: true,
      requestedTransitionTo: "CURATING",
    }),
    actionDescriptor({
      id: "TARGET_REMOVE",
      alias: "@Target-Remove",
      bodySchema: "NONE",
      subjectType: "TARGET",
      subjectIDRequired: true,
      transcriptPolicy: "PATCH_ONLY",
    }),
  ],
  timeline: [
    {
      id: "timeline-action-1",
      kind: "ACTION",
      createdAt: "2026-07-31T01:00:00Z",
      command: "@Intent-NextStep: 집중할 수 있는 업무 환경",
    },
    {
      id: "timeline-result-1",
      kind: "RESULT",
      createdAt: "2026-07-31T01:01:00Z",
      summary: "첫 Target 목록을 만들었습니다.",
      artifact: {
        id: "artifact-1",
        kind: "TARGET_LIST",
        title: "초기 Target 목록",
        updatedAt: "2026-07-31T01:01:00Z",
        diff: { added: ["업무용 의자", "책상"] },
      },
    },
    {
      id: "timeline-action-2",
      kind: "ACTION",
      createdAt: "2026-07-31T02:00:00Z",
      command:
        "@TargetList:curation-1-AddTarget: 업무용 조명도 추가",
      action: {
        id: "timeline-action-2",
        curationId: "curation-1",
        type: "PLANNING_ADD_TARGETS",
        phaseAtRequest: "PLANNING",
        subjectType: "TARGET_LIST",
        subjectId: "curation-1",
        effectKind: "INTELLIGENCE",
        expectedCurationVersion: 3,
        createdAt: "2026-07-31T02:00:00Z",
      },
    },
    {
      id: "timeline-result-2",
      kind: "RESULT",
      createdAt: "2026-07-31T02:02:00Z",
      summary: "Target 3개를 검토할 수 있습니다.",
      artifact: {
        id: "artifact-2",
        kind: "TARGET_LIST",
        title: "업무 환경 큐레이션",
        summary: "예산과 공간 조건을 반영한 현재 Target입니다.",
        updatedAt: "2026-07-31T02:02:00Z",
        targets: [
          {
            id: "target-1",
            title: "업무용 의자",
            candidateCount: 0,
            status: "READY",
          },
          {
            id: "target-2",
            title: "책상",
            candidateCount: 0,
            status: "READY",
          },
          {
            id: "target-3",
            title: "업무용 조명",
            candidateCount: 0,
            status: "READY",
          },
        ],
      },
    },
  ],
  cart: {
    curationId: "curation-1",
    selections: [],
    total: { amount: "0", currency: "USD" },
    warnings: [],
    updatedAt: "2026-07-31T04:00:00Z",
  },
};

function actionDescriptor(
  input: Pick<
    CurationActionDescriptor,
    "id" | "alias" | "bodySchema"
  > & {
    subjectType: CurationActionDescriptor["subjectSchema"]["type"];
    subjectIDRequired?: boolean;
    transcriptPolicy?: CurationActionDescriptor["transcriptPolicy"];
    requestedTransitionTo?: CurationActionDescriptor["requestedTransitionTo"];
  },
): CurationActionDescriptor {
  return {
    id: input.id,
    alias: input.alias,
    enabled: true,
    subjectSchema: {
      type: input.subjectType,
      idRequired: input.subjectIDRequired ?? false,
    },
    bodySchema: input.bodySchema,
    effectKind:
      input.id === "PLANNING_START_CURATING" ? "INTELLIGENCE" : "NONE",
    transcriptPolicy: input.transcriptPolicy ?? "APPEND",
    expectedResourceVersion: 4,
    requiresConfirmation: false,
    requestedTransitionTo: input.requestedTransitionTo,
  };
}
