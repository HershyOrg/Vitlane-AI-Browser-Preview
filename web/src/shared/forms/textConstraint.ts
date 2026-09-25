import type { Localize } from "../i18n";

// 길이 제약이 있는 텍스트 입력의 피드백 계약. 서버와 같은 코드포인트 길이(trim 뒤)를
// 재며, 규칙은 hint로 항상 보이고 조건 미달은 error로 승격해 Field가 danger 색으로
// 강조한다. "필수인데 비어 있음"은 사용자가 필드를 한 번 만진(touched) 뒤에만 error다
// — 첫 화면이 온통 붉지 않게 하되, 비활성 버튼의 이유는 필드에서 바로 읽히게 한다.
export type TextConstraintInput = {
  value: string;
  max: number;
  min?: number;
  required?: boolean;
  touched?: boolean;
  l: Localize;
};

export type TextConstraint = {
  length: number;
  ready: boolean;
  hint: string;
  error?: string;
};

// Digits are grouped the same way as the rest of the product copy ("2,000자").
const fmt = (value: number) => value.toLocaleString("en-US");

export function textLength(value: string): number {
  return Array.from(value.trim()).length;
}

export function textConstraint({ value, max, min = 0, required = min > 0, touched = false, l }: TextConstraintInput): TextConstraint {
  const length = textLength(value);
  const effectiveMin = required ? Math.max(min, 1) : min;
  const hint = effectiveMin > 1
    ? l("{min}–{max} characters · {length} now", "{min}~{max}자 · 현재 {length}자", { min: fmt(effectiveMin), max: fmt(max), length: fmt(length) })
    : effectiveMin === 1
      ? l("1–{max} characters · {length} now", "1~{max}자 · 현재 {length}자", { max: fmt(max), length: fmt(length) })
      : l("Up to {max} characters · {length} now", "최대 {max}자 · 현재 {length}자", { max: fmt(max), length: fmt(length) });
  let error: string | undefined;
  if (length > max) {
    error = l("Use {max} characters or fewer ({over} over).", "{max}자 이내로 입력하세요 ({over}자 초과).", { max: fmt(max), over: fmt(length - max) });
  } else if (length > 0 && length < effectiveMin) {
    error = l("Enter at least {min} characters ({missing} more needed).", "{min}자 이상 입력하세요 ({missing}자 더 필요).", { min: fmt(effectiveMin), missing: fmt(effectiveMin - length) });
  } else if (length === 0 && required && touched) {
    error = l("Required.", "필수 입력입니다.");
  }
  const ready = length <= max && length >= effectiveMin;
  return { length, ready, hint, error };
}
