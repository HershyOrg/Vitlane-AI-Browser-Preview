import { describe, expect, it } from "vitest";
import {
  CurationCommandError,
  commandSeed,
  filterCurationActionAutocomplete,
  parseCurationCommand,
  serializeCurationCommand,
} from "./command";
import type {
  CurationActionDescriptor,
  CurationCommandChoice,
} from "./types";

const actions: CurationActionDescriptor[] = [
  descriptor({
    id: "PLANNING_ADD_TARGETS",
    alias: "@TargetList-AddTarget",
    bodySchema: "TEXT_REQUIRED",
    subjectIDRequired: true,
  }),
  descriptor({
    id: "PLANNING_START_CURATING",
    alias: "@Planning-NextStep",
    bodySchema: "NONE",
    subjectType: "CURATION",
    subjectIDRequired: true,
  }),
  descriptor({
    id: "TARGET_REMOVE",
    alias: "@Target-Remove",
    bodySchema: "NONE",
    subjectType: "TARGET",
    subjectIDRequired: true,
    transcriptPolicy: "PATCH_ONLY",
  }),
];

describe("curation command", () => {
  it("서버 catalog의 case-sensitive alias와 정확한 ': ' 구분자만 파싱한다", () => {
    const parsed = parseCurationCommand(
      "@TargetList:11111111-1111-4111-8111-111111111111-AddTarget: 휴대용 모니터도 추가해줘",
      actions,
    );

    expect(parsed.descriptor.id).toBe("PLANNING_ADD_TARGETS");
    expect(parsed.alias).toContain("@TargetList:");
    expect(parsed.subjectId).toBe(
      "11111111-1111-4111-8111-111111111111",
    );
    expect(parsed.body).toBe("휴대용 모니터도 추가해줘");
    expect(
      parseCurationCommand(
        "@Planning:22222222-2222-4222-8222-222222222222-NextStep",
        actions,
      ).body,
    ).toBe("");
  });

  it.each([
    "다음 단계로 진행해줘",
    "타깃을 추가해줘",
    "@targetList-AddTarget: 조명",
    "@TargetList-AddTarget:조명",
    "@TargetList-AddTarget:  조명",
    " @TargetList-AddTarget: 조명",
    "@TargetList-AddTarget: 조명 ",
    "@Candidate:44444444-4444-4444-8444-444444444444-PurchaseDirect",
    "@Curation:22222222-2222-4222-8222-222222222222-PurchaseCart",
  ])("자연어 추론이나 느슨한 명령 형식을 허용하지 않는다: %s", (input) => {
    expect(() => parseCurationCommand(input, actions)).toThrow(
      CurationCommandError,
    );
  });

  it("body schema를 serializer와 parser 양쪽에서 검사한다", () => {
    expect(() =>
      serializeCurationCommand(
        actions[0],
        "",
        "11111111-1111-4111-8111-111111111111",
      ),
    ).toThrowError(/필요한 내용을/);
    expect(() =>
      serializeCurationCommand(
        actions[1],
        "지금",
        "22222222-2222-4222-8222-222222222222",
      ),
    ).toThrowError(/추가 내용을/);
    expect(
      serializeCurationCommand(
        actions[0],
        "조명 추가",
        "11111111-1111-4111-8111-111111111111",
      ),
    ).toBe(
      "@TargetList:11111111-1111-4111-8111-111111111111-AddTarget: 조명 추가",
    );
    expect(
      serializeCurationCommand(
        actions[1],
        "",
        "22222222-2222-4222-8222-222222222222",
      ),
    ).toBe(
      "@Planning:22222222-2222-4222-8222-222222222222-NextStep",
    );
    expect(
      commandSeed(
        actions[0],
        "11111111-1111-4111-8111-111111111111",
      ),
    ).toBe(
      "@TargetList:11111111-1111-4111-8111-111111111111-AddTarget: ",
    );
    expect(
      commandSeed(
        actions[2],
        "33333333-3333-4333-8333-333333333333",
      ),
    ).toBe(
      "@Target:33333333-3333-4333-8333-333333333333-Remove",
    );
  });

  it("자동완성은 @로 시작하는 exact prefix만 catalog 순서로 필터링한다", () => {
    const choices = commandChoices(actions);
    expect(
      filterCurationActionAutocomplete("@Target", choices).map(
        ({ alias }) => alias,
      ),
    ).toEqual([
      "@TargetList:11111111-1111-4111-8111-111111111111-AddTarget",
      "@Target:33333333-3333-4333-8333-333333333333-Remove",
    ]);
    expect(filterCurationActionAutocomplete("@target", choices)).toEqual([]);
    expect(
      filterCurationActionAutocomplete("타깃 추가", choices),
    ).toEqual([]);
    expect(
      filterCurationActionAutocomplete(
        "@TargetList:11111111-1111-4111-8111-111111111111-AddTarget: 조명",
        choices,
      ),
    ).toEqual([]);
  });

  it("disabled catalog 항목은 파싱 시 서버의 사유로 거절한다", () => {
    const disabled = {
      ...actions[1],
      enabled: false,
      unavailableReason: "Target을 먼저 추가해 주세요.",
    };
    expect(() =>
      parseCurationCommand(
        "@Planning:22222222-2222-4222-8222-222222222222-NextStep",
        [disabled],
      ),
    ).toThrowError("Target을 먼저 추가해 주세요.");
  });
});

function commandChoices(
  catalog: CurationActionDescriptor[],
): CurationCommandChoice[] {
  const ids = {
    TARGET_LIST: "11111111-1111-4111-8111-111111111111",
    CURATION: "22222222-2222-4222-8222-222222222222",
    TARGET: "33333333-3333-4333-8333-333333333333",
    CANDIDATE: "44444444-4444-4444-8444-444444444444",
  };
  return catalog.map((descriptor) => {
    const subjectId = ids[descriptor.subjectSchema.type as keyof typeof ids];
    const separator = descriptor.alias.indexOf("-");
    const alias = descriptor.subjectSchema.idRequired
      ? `${descriptor.alias.slice(0, separator)}:${subjectId}${descriptor.alias.slice(separator)}`
      : descriptor.alias;
    return {
      key: descriptor.id,
      descriptor,
      alias: alias as CurationActionDescriptor["alias"],
      subjectId,
    };
  });
}

function descriptor(
  input: Pick<
    CurationActionDescriptor,
    "id" | "alias" | "bodySchema"
  > & {
    subjectType?: CurationActionDescriptor["subjectSchema"]["type"];
    subjectIDRequired?: boolean;
    transcriptPolicy?: CurationActionDescriptor["transcriptPolicy"];
  },
): CurationActionDescriptor {
  return {
    id: input.id,
    alias: input.alias,
    enabled: true,
    subjectSchema: {
      type: input.subjectType ?? "TARGET_LIST",
      idRequired: input.subjectIDRequired ?? false,
    },
    bodySchema: input.bodySchema,
    effectKind: "NONE",
    transcriptPolicy: input.transcriptPolicy ?? "APPEND",
    expectedResourceVersion: 3,
    requiresConfirmation: false,
  };
}
