import type {
  CurationActionBodySchema,
  CurationCommandChoice,
  CurationActionDescriptor,
  ParsedCurationCommand,
} from "./types";
import { localizeFixedCopy } from "../../../shared/i18n";

export type CurationCommandErrorCode =
  | "COMMAND_INVALID"
  | "ALIAS_UNKNOWN"
  | "ACTION_UNAVAILABLE"
  | "BODY_INVALID";

export class CurationCommandError extends Error {
  readonly code: CurationCommandErrorCode;

  constructor(code: CurationCommandErrorCode, message: string) {
    super(message);
    this.name = "CurationCommandError";
    this.code = code;
  }
}

/**
 * Parse only the exact, case-sensitive alias returned by the server catalog.
 * This function intentionally performs no fuzzy matching or natural-language
 * action inference.
 */
export function parseCurationCommand(
  command: string,
  catalog: readonly CurationActionDescriptor[],
): ParsedCurationCommand {
  if (
    command === "" ||
    command.trim() !== command ||
    command.includes("\0")
  ) {
    throw invalidCommand();
  }

  const colonIndex = command.indexOf(": ");
  let alias = command;
  let body = "";
  let hasBody = false;

  if (colonIndex >= 0) {
    alias = command.slice(0, colonIndex);
    body = command.slice(colonIndex + 2);
    if (body === "" || body.trim() !== body) {
      throw new CurationCommandError(
        "BODY_INVALID",
        localizeFixedCopy("Enter the command body without leading or trailing spaces.", "명령 뒤의 내용을 앞뒤 공백 없이 입력해 주세요."),
      );
    }
    hasBody = true;
  }

  const matched = catalog
    .map((descriptor) => ({
      descriptor,
      subjectId: subjectIDFromAlias(alias, descriptor),
    }))
    .find(({ subjectId }) => subjectId !== null);
  if (!matched) {
    throw new CurationCommandError(
      "ALIAS_UNKNOWN",
      localizeFixedCopy("Select an exact @command available at the current stage.", "현재 단계에서 사용할 수 있는 정확한 @명령을 선택해 주세요."),
    );
  }
  const { descriptor } = matched;
  if (!descriptor.enabled) {
    throw new CurationCommandError(
      "ACTION_UNAVAILABLE",
      descriptor.unavailableReason ?? localizeFixedCopy("This action is not available now.", "현재는 이 행동을 실행할 수 없습니다."),
    );
  }

  validateBody(descriptor.bodySchema, body, hasBody);
  return {
    descriptor,
    alias: alias as CurationActionDescriptor["alias"],
    subjectId: matched.subjectId || undefined,
    body,
  };
}

export function serializeCurationCommand(
  descriptor: CurationActionDescriptor,
  body = "",
  subjectId?: string,
): string {
  const alias = commandAlias(descriptor, subjectId);
  const hasBody = body !== "";
  validateBody(descriptor.bodySchema, body, hasBody);
  return hasBody ? `${alias}: ${body}` : alias;
}

/**
 * Autocomplete is prefix-only and case-sensitive. Text without a leading @
 * never produces an action candidate.
 */
export function filterCurationActionAutocomplete(
  input: string,
  choices: readonly CurationCommandChoice[],
): CurationCommandChoice[] {
  if (
    !input.startsWith("@") ||
    input.trim() !== input ||
    input.includes("\0") ||
    input.includes(": ")
  ) {
    return [];
  }
  return choices.filter(({ alias }) => alias.startsWith(input));
}

export function commandSeed(
  descriptor: CurationActionDescriptor,
  subjectId?: string,
): string {
  const alias = commandAlias(descriptor, subjectId);
  return commandBodyRequired(descriptor.bodySchema)
    ? `${alias}: `
    : alias;
}

export function commandAlias(
  descriptor: CurationActionDescriptor,
  subjectId?: string,
): CurationActionDescriptor["alias"] {
  const parts = aliasParts(descriptor.alias);
  if (!parts) throw invalidCommand();
  if (!descriptor.subjectSchema.idRequired) {
    if (subjectId) throw invalidCommand();
    return descriptor.alias;
  }
  if (!validSubjectID(subjectId)) {
    throw new CurationCommandError(
      "COMMAND_INVALID",
      localizeFixedCopy("Use an @command that includes the target ID.", "대상 ID가 포함된 @명령을 사용해 주세요."),
    );
  }
  return `@${parts.subject}:${subjectId}-${parts.action}`;
}

function validateBody(
  schema: CurationActionBodySchema,
  body: string,
  hasBody: boolean,
) {
  if (schema === "NONE") {
    if (hasBody || body !== "") {
      throw new CurationCommandError(
        "BODY_INVALID",
        localizeFixedCopy("This action does not accept additional content.", "이 행동에는 추가 내용을 입력할 수 없습니다."),
      );
    }
    return;
  }

  if (commandBodyRequired(schema) && !validPresentBody(body, hasBody)) {
    throw new CurationCommandError(
      "BODY_INVALID",
      localizeFixedCopy("Enter the content required for this action.", "이 행동에 필요한 내용을 입력해 주세요."),
    );
  }
  if (hasBody && !validPresentBody(body, true)) {
    throw new CurationCommandError(
      "BODY_INVALID",
      localizeFixedCopy("Enter the command body without leading or trailing spaces.", "명령 뒤의 내용을 앞뒤 공백 없이 입력해 주세요."),
    );
  }
  if (!hasBody && body !== "") {
    throw new CurationCommandError(
      "BODY_INVALID",
      localizeFixedCopy("Review the command content.", "명령 내용을 확인해 주세요."),
    );
  }
}

function commandBodyRequired(schema: CurationActionBodySchema) {
  return schema === "TEXT_REQUIRED" || schema === "TYPED_COMMAND";
}

function validPresentBody(body: string, hasBody: boolean) {
  return (
    hasBody &&
    body !== "" &&
    body.trim() === body &&
    !body.includes("\0")
  );
}

function subjectIDFromAlias(
  alias: string,
  descriptor: CurationActionDescriptor,
): string | null {
  const parts = aliasParts(descriptor.alias);
  if (!parts || /\s/.test(alias)) return null;
  if (!descriptor.subjectSchema.idRequired) {
    return alias === descriptor.alias ? "" : null;
  }
  const prefix = `@${parts.subject}:`;
  const suffix = `-${parts.action}`;
  if (!alias.startsWith(prefix) || !alias.endsWith(suffix)) return null;
  const subjectId = alias.slice(prefix.length, -suffix.length);
  return validSubjectID(subjectId) ? subjectId : null;
}

function aliasParts(alias: string) {
  if (!alias.startsWith("@") || /\s/.test(alias)) return null;
  const separator = alias.indexOf("-");
  if (separator <= 1 || separator >= alias.length - 1) return null;
  return {
    subject: alias.slice(1, separator),
    action: alias.slice(separator + 1),
  };
}

function validSubjectID(value: string | undefined): value is string {
  return Boolean(
    value &&
      value.trim() === value &&
      !/\s/.test(value) &&
      !value.includes("\0") &&
      !value.includes(":"),
  );
}

function invalidCommand() {
  return new CurationCommandError(
    "COMMAND_INVALID",
    localizeFixedCopy(
      "Enter commands as @target:<ID>-action or @target:<ID>-action: content.",
      "명령은 @대상:<ID>-행동 또는 @대상:<ID>-행동: 내용 형식으로 입력해 주세요.",
    ),
  );
}
