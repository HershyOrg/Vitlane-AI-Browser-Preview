// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ManagedRunnerUsageSummary } from "./ManagedRunnerUsageSummary";

const usage = vi.hoisted(() => ({
  calls: 0,
  value: {
    enabled: true,
    usageDate: "2026-07-31",
    userSpentMicros: 25_000,
    userLimitMicros: 100_000,
    userExhausted: false,
    serverExhausted: false,
    serverLimitMicros: 10_000_000,
  },
}));

vi.mock("../../curation/planning/infra/planningApi", () => ({
  fetchManagedRunnerUsage: () => {
    usage.calls += 1;
    return Promise.resolve(usage.value);
  },
}));

async function render(open: boolean) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(<ManagedRunnerUsageSummary open={open} />);
  });
  return {
    container,
    reopen: async (next: boolean) => {
      await act(async () => {
        root.render(<ManagedRunnerUsageSummary open={next} />);
      });
    },
  };
}

describe("ManagedRunnerUsageSummary", () => {
  afterEach(() => {
    document.body.innerHTML = "";
    usage.calls = 0;
  });

  it("사용량을 금액이 아니라 %로 보여준다", async () => {
    const { container } = await render(true);

    // The user is never billed, so an amount would invite them to reason about
    // a cost they do not pay.
    expect(container.textContent).toContain("25%");
    expect(container.textContent).toContain("75%");
    expect(container.textContent).not.toContain("$");
  });

  it("열 때마다 다시 조회한다", async () => {
    const { reopen } = await render(true);
    expect(usage.calls).toBe(1);

    await reopen(false);
    await reopen(true);

    // The menu is a <details> whose children stay mounted, so a fetch tied to
    // mount alone would still be showing the figure from page load after a
    // curation had spent against the allowance.
    expect(usage.calls).toBe(2);
  });

  it("닫혀 있을 때는 조회하지 않는다", async () => {
    await render(false);

    expect(usage.calls).toBe(0);
  });

  it("한도를 다 쓰면 이유를 알린다", async () => {
    usage.value = {
      ...usage.value,
      userSpentMicros: 100_000,
      userExhausted: true,
    };
    const { container } = await render(true);

    expect(container.textContent).toContain("100%");
    expect(container.textContent).toContain("오늘 한도를 모두 사용했습니다");

    usage.value = { ...usage.value, userSpentMicros: 25_000, userExhausted: false };
  });

  it("ManagedRunner가 꺼져 있으면 아무것도 그리지 않는다", async () => {
    usage.value = { ...usage.value, enabled: false };
    const { container } = await render(true);

    expect(container.textContent).toBe("");

    usage.value = { ...usage.value, enabled: true };
  });
});
