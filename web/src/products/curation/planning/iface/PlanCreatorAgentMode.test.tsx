// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initialPlanForm } from "../domain/form";
import { PlanCreator } from "./PlanCreator";

const capability = vi.hoisted(() => ({
  value: {
    enabled: true,
    models: [
      { key: "gpt-5-nano", label: "빠름 · gpt-5-nano" },
      { key: "gpt-5.6-luna", label: "정밀 · gpt-5.6-luna" },
    ],
    defaultModelKey: "gpt-5.6-luna",
    serverExhausted: false,
  },
}));

vi.mock("../infra/planningApi", () => ({
  fetchManagedRunnerCapability: () => Promise.resolve(capability.value),
}));

async function renderCreator(onSubmit = vi.fn().mockResolvedValue(undefined)) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <PlanCreator
        initialForm={initialPlanForm}
        working={false}
        onSubmit={onSubmit}
      />,
    );
  });
  const settings = container.querySelector<HTMLButtonElement>(
    '[aria-controls="shell-intent-settings"]',
  );
  await act(async () => settings?.click());
  return { container, onSubmit, root };
}

function agentModeToggle(container: HTMLElement) {
  return container.querySelector<HTMLButtonElement>(
    '.shell-agent-mode__toggle [role="switch"]',
  );
}

function typeIntent(container: HTMLElement, text: string) {
  const intent = container.querySelector<HTMLTextAreaElement>(
    "#curation-intent",
  );
  if (intent) {
    const setter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      "value",
    )?.set;
    setter?.call(intent, text);
    intent.dispatchEvent(new Event("input", { bubbles: true }));
  }
}

describe("PlanCreator agent mode", () => {
  beforeEach(() => {
    capability.value = {
      enabled: true,
      models: [
        { key: "gpt-5-nano", label: "빠름 · gpt-5-nano" },
        { key: "gpt-5.6-luna", label: "정밀 · gpt-5.6-luna" },
      ],
      defaultModelKey: "gpt-5.6-luna",
      serverExhausted: false,
    };
  });

  afterEach(() => {
    document.body.innerHTML = "";
    window.localStorage.clear();
  });

  it("ManagedAgent가 유일한 실행 주체로 고정되고 베타 경고가 없다", async () => {
    const { container } = await renderCreator();

    expect(agentModeToggle(container)).toBeNull();
    expect(container.textContent).not.toContain("준비 중");
    expect(container.querySelector('[aria-label="조사 모델"]')).not.toBeNull();
  });

  it("이전에 외부 Agent를 골랐어도 다시 열면 ManagedAgent로 시작한다", async () => {
    // A stored EXTERNAL from an earlier curation must not survive the
    // retirement: the composer always reopens on the managed owner.
    window.localStorage.setItem(
      "vitlane.plan-preferences.v1",
      JSON.stringify({
        planningMode: "SINGLE",
        currency: "KRW",
        minPrice: "2500",
        maxPrice: "5000",
        country: "JP",
        city: "도쿄",
        agentMode: "EXTERNAL",
        modelKey: "gpt-5-nano",
      }),
    );

    const { container } = await renderCreator();

    expect(agentModeToggle(container)).toBeNull();
    // The rest of the remembered settings still carry over.
    expect(
      container.querySelector('[aria-label="예산 설정"]')?.textContent,
    ).toContain("자동");
  });

  it("서버가 더 이상 제공하지 않는 기억된 모델은 기본 모델로 바뀐다", async () => {
    window.localStorage.setItem(
      "vitlane.plan-preferences.v1",
      JSON.stringify({ modelKey: "gpt-4-retired" }),
    );

    const { container } = await renderCreator();

    // Submitting a MANAGED plan against a model the Server no longer runs
    // would fail after the user had already committed the intent.
    expect(
      container.querySelector<HTMLSelectElement>('[aria-label="조사 모델"]')
        ?.value,
    ).toBe("gpt-5.6-luna");
  });

  it("저장된 모델이 없으면 서버 기본값과 관계없이 Luna로 시작한다", async () => {
    capability.value = {
      ...capability.value,
      defaultModelKey: "gpt-5-nano",
    };

    const { container } = await renderCreator();

    expect(
      container.querySelector<HTMLSelectElement>('[aria-label="조사 모델"]')
        ?.value,
    ).toBe("gpt-5.6-luna");
  });

  it("모델 선택은 설정 패널을 닫아도 채팅 입력부에 남는다", async () => {
    const { container } = await renderCreator();
    const settings = container.querySelector<HTMLButtonElement>(
      '[aria-controls="shell-intent-settings"]',
    );

    await act(async () => settings?.click());

    const model = container.querySelector<HTMLSelectElement>(
      '[aria-label="조사 모델"]',
    );
    expect(settings).toBeNull();
    expect(model).not.toBeNull();
    expect(model?.value).toBe("gpt-5.6-luna");
    expect(model?.closest("#shell-intent-settings")).toBeNull();
  });

  it("비어 있는 저장값은 선택으로 취급하지 않고 Luna로 복구한다", async () => {
    window.localStorage.setItem(
      "vitlane.plan-preferences.v1",
      JSON.stringify({ modelKey: "" }),
    );
    capability.value = {
      ...capability.value,
      defaultModelKey: "gpt-5-nano",
    };

    const { container } = await renderCreator();

    expect(
      container.querySelector<HTMLSelectElement>('[aria-label="조사 모델"]')
        ?.value,
    ).toBe("gpt-5.6-luna");
  });

  it("성공적으로 사용한 모델은 다음 요청의 로컬 기본값이 된다", async () => {
    const { container, onSubmit, root } = await renderCreator();
    const model = container.querySelector<HTMLSelectElement>(
      '[aria-label="조사 모델"]',
    );
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLSelectElement.prototype,
      "value",
    )?.set;
    await act(async () => {
      valueSetter?.call(model, "gpt-5-nano");
      model?.dispatchEvent(new Event("change", { bubbles: true }));
    });
    typeIntent(container, "휴대용 모니터");

    await act(async () => {
      container.querySelector<HTMLFormElement>("form")?.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      await Promise.resolve();
    });

    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ modelKey: "gpt-5-nano" }),
    );
    expect(
      JSON.parse(
        window.localStorage.getItem("vitlane.plan-preferences.v1") ?? "{}",
      ).modelKey,
    ).toBe("gpt-5-nano");

    await act(async () => root.unmount());
    const { container: nextContainer } = await renderCreator();
    expect(
      nextContainer.querySelector<HTMLSelectElement>(
        '[aria-label="조사 모델"]',
      )?.value,
    ).toBe("gpt-5-nano");
  });

  it("토글을 눌러도 외부 Agent로 전환되지 않는다", async () => {
    const { container, onSubmit } = await renderCreator();

    const toggle = agentModeToggle(container);
    await act(async () => toggle?.click());

    expect(toggle).toBeNull();
    expect(
      container.querySelector('[aria-label="조사 모델"]'),
    ).not.toBeNull();

    typeIntent(container, "캠핑 의자");
    await act(async () => {
      container
        .querySelector<HTMLFormElement>("form")
        ?.dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        );
    });

    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        agentMode: "MANAGED",
        modelKey: "gpt-5.6-luna",
      }),
    );
  });

  it("서버가 ManagedAgent를 끄면 제출이 막힌다", async () => {
    capability.value = {
      enabled: false,
      models: [],
      defaultModelKey: "",
      serverExhausted: false,
    };

    const { container } = await renderCreator();

    // With the external path retired there is nothing to fall back to, so
    // the composer blocks submission instead of switching owners.
    expect(container.textContent).toContain(
      "현재 조사를 시작할 수 없습니다.",
    );
    typeIntent(container, "캠핑 의자");
    await act(async () => {});
    const submit = container.querySelector<HTMLButtonElement>(
      ".shell-intent-composer__submit",
    );
    expect(submit?.disabled).toBe(true);
  });

  it("서버 일일 한도가 차면 제출을 막고 이유를 알린다", async () => {
    capability.value = { ...capability.value, serverExhausted: true };

    const { container } = await renderCreator();

    typeIntent(container, "캠핑 의자");
    await act(async () => {});

    // Submitting here would create a curation whose first work order fails
    // immediately, which reads as a product bug rather than a quota.
    const submit = container.querySelector<HTMLButtonElement>(
      ".shell-intent-composer__submit",
    );
    expect(submit?.disabled).toBe(true);
    expect(container.textContent).toContain("오늘의 AI 사용 한도에 도달했습니다.");
  });
});
