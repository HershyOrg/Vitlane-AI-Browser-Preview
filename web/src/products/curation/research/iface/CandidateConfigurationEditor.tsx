import { useMemo, useState } from "react";
import type {
  Candidate,
  CandidateConfigurationInput,
  Orderability,
  VariantDiscoveryStatus,
  VariantField,
  VariantInputKind,
} from "../../../../shared/api/types";
import {
  Button,
  Checkbox,
  Input,
  NativeSelect,
  NativeSelectOption,
  Switch,
} from "../../../../shared/ui";
import { invariantContent, useLocale, type Localize } from "../../../../shared/i18n";

type Props = {
  candidate: Candidate;
  working: boolean;
  confirmLabel?: string;
  workingLabel?: string;
  onCancel: () => void;
  onConfirm: (configuration: CandidateConfigurationInput) => Promise<void>;
};

function pathCopy(status: Orderability["status"], l: Localize) {
  return {
    TEST_ORDER_FLOW_AVAILABLE: l("TEST order flow available", "TEST 주문 흐름 진행 가능"),
    OPTIONS_REQUIRED: l("Option input required", "옵션 입력 필요"),
    PRICE_RECHECK_REQUIRED: l("Price recheck required", "가격 재확인 필요"),
    SETTLEMENT_CURRENCY_UNSUPPORTED: l("Settlement currency not supported", "현재 정산 통화 미지원"),
    POLICY_BLOCKED: l("Blocked by policy", "정책상 차단"),
  }[status];
}

function discoveryCopy(status: VariantDiscoveryStatus, l: Localize) {
  return {
    NOT_APPLICABLE: l("No options", "옵션 없음"),
    OBSERVED_PARTIAL: l("Some options found", "옵션 일부 확인됨"),
    COMPLETE_UNVERIFIED: l("Option structure found · Combination unverified", "옵션 구조 확인 · 조합 미검증"),
    PROVIDER_VERIFIED: l("Options verified automatically", "옵션 자동 확인됨"),
    UNKNOWN: l("Enter your preferred options", "원하는 옵션을 직접 입력하세요"),
  }[status];
}

export function candidatePath(candidate: Candidate): Orderability {
  return candidate.orderability;
}

export function candidateVariantFields(candidate: Candidate): VariantField[] {
  return candidate.variantDiscovery.fields.map(copyField);
}

function copyField(field: VariantField): VariantField {
  return {
    ...field,
    knownValues: field.knownValues.map((value) => ({ ...value })),
  };
}

export function CandidatePathRail({ candidate }: { candidate: Candidate }) {
  const { l } = useLocale();
  const path = candidatePath(candidate);
  const discoveryStatus = candidate.variantDiscovery.status;
  const displayStatus =
    path.status === "TEST_ORDER_FLOW_AVAILABLE" &&
    (discoveryStatus === "UNKNOWN" || discoveryStatus === "OBSERVED_PARTIAL")
      ? "OPTIONS_REQUIRED"
      : path.status;

  return (
    <div className="candidate-path-rail" aria-label={l("Purchase readiness", "구매 처리 가능 상태")}>
      <span className={`candidate-path-state is-${displayStatus.toLowerCase()}`}>
        {pathCopy(displayStatus, l)}
      </span>
      <span>{path.providerKind}</span>
      <span>{path.executionMode}</span>
      <span>{path.externalEffect}</span>
      <small>
        {discoveryCopy(discoveryStatus, l)} · {l("Actual merchant options are checked again during order processing.", "실제 판매 옵션은 주문 처리 단계에서 다시 확인합니다.")}
      </small>
    </div>
  );
}

export function CandidateConfigurationEditor({
  candidate,
  working,
  confirmLabel,
  workingLabel,
  onCancel,
  onConfirm,
}: Props) {
  const { l } = useLocale();
  const resolvedConfirmLabel = confirmLabel ?? l("Continue with these options", "이 옵션으로 계속");
  const resolvedWorkingLabel = workingLabel ?? l("Processing options…", "옵션 처리 중…");
  const discovery = candidate.variantDiscovery;
  const originalFields = useMemo(() => candidateVariantFields(candidate), [candidate]);
  const [fields, setFields] = useState<VariantField[]>(originalFields);
  const [selections, setSelections] = useState<Record<string, string>>({});
  const [confirmsNoOptions, setConfirmsNoOptions] = useState(false);
  const [intentConfirmed, setIntentConfirmed] = useState(false);
  const [showErrors, setShowErrors] = useState(false);

  const errors = validateConfiguration(fields, selections, confirmsNoOptions, l);
  const canConfirm =
    errors.length === 0 &&
    intentConfirmed &&
    candidatePath(candidate).status !== "POLICY_BLOCKED" &&
    candidatePath(candidate).status !== "SETTLEMENT_CURRENCY_UNSUPPORTED";

  function updateField(index: number, patch: Partial<VariantField>) {
    setFields((current) =>
      current.map((field, fieldIndex) =>
        fieldIndex === index ? { ...field, ...patch, source: "USER" } : field,
      ),
    );
  }

  function removeField(index: number) {
    const removedKey = fields[index]?.key;
    setFields((current) => current.filter((_, fieldIndex) => fieldIndex !== index));
    if (removedKey) {
      setSelections((current) => {
        const next = { ...current };
        delete next[removedKey];
        return next;
      });
    }
  }

  function changeKey(index: number, nextKey: string) {
    const previousKey = fields[index].key;
    updateField(index, { key: nextKey });
    setSelections((current) => {
      if (!(previousKey in current)) return current;
      const next = { ...current, [nextKey]: current[previousKey] };
      delete next[previousKey];
      return next;
    });
  }

  function addField() {
    const suffix = fields.length + 1;
    setConfirmsNoOptions(false);
    setFields((current) => [
      ...current,
      {
        key: `option_${suffix}`,
        label: invariantContent("새 옵션"),
        inputKind: "VALUE",
        required: true,
        knownValues: [],
        source: "USER",
        discoveryStatus: "UNKNOWN",
      },
    ]);
  }

  return (
    <form
      className="candidate-configuration"
      onSubmit={(event) => {
        event.preventDefault();
        setShowErrors(true);
        if (!canConfirm) return;
        void onConfirm({
          fields: confirmsNoOptions ? [] : fields.map(copyField),
          selections: confirmsNoOptions ? {} : selections,
          confirmsNoOptions,
        });
      }}
    >
      <header>
        <div>
          <p className="utility-label">{l("Your order intent / separate snapshot", "Your order intent / separate snapshot")}</p>
          <h4>{l("Confirm the options you want", "원하는 옵션을 확정하세요")}</h4>
        </div>
        <span>{discoveryCopy(discovery.status, l)}</span>
      </header>

      <div className="variant-provenance">
        <span><b>01</b> {l("Agent observation", "Agent 관찰")}</span>
        <i aria-hidden="true" />
        <span className="is-current"><b>02</b> {l("User confirmation", "사용자 확정")}</span>
        <i aria-hidden="true" />
        <span><b>03</b> {l("Operator validation", "운영자 검증")}</span>
        <small>
          {l("The Agent submission is not modified. Your selections are preserved as a separate configuration.", "Agent 제출은 수정하지 않습니다. 지금 입력한 선택은 별도 configuration으로 보존됩니다.")}
        </small>
      </div>

      {discovery.evidence?.summary && (
        <p className="variant-evidence">
          <strong>{l("Observation evidence", "관찰 근거")}</strong>
          {discovery.evidence.summary}
        </p>
      )}

      <div className="no-variant-confirmation">
        <Checkbox
          id="no-variant-confirmation"
          checked={confirmsNoOptions}
          onCheckedChange={(checked) => setConfirmsNoOptions(checked === true)}
        />
        <label htmlFor="no-variant-confirmation">
          <strong>{l("This product has no options for me to choose", "이 상품에는 내가 선택할 옵션이 없습니다")}</strong>
          <small>{l("Confirm only when there truly are no order choices such as color, size, or voltage.", "색상·크기·전압처럼 주문에 필요한 선택이 정말 없을 때만 확인하세요.")}</small>
        </label>
      </div>

      {!confirmsNoOptions && (
        <div className="variant-field-list">
          {fields.map((field, index) => (
            <VariantFieldEditor
              key={index}
              field={field}
              index={index}
              value={selections[field.key] ?? ""}
              onFieldChange={(patch) => updateField(index, patch)}
              onKeyChange={(key) => changeKey(index, key)}
              onValueChange={(value) =>
                setSelections((current) => ({ ...current, [field.key]: value }))
              }
              onRemove={() => removeField(index)}
            />
          ))}
          <Button
            className="variant-add-field"
            emphasis="quiet"
            onClick={addField}
          >
            <span aria-hidden="true">＋</span> {l("Add an option the Agent did not find", "Agent가 찾지 못한 옵션 추가")}
          </Button>
        </div>
      )}

      {showErrors && errors.length > 0 && (
        <div className="variant-validation" role="alert">
          <strong>{l("Review before confirming", "확정 전에 확인할 항목")}</strong>
          <ul>{errors.map((error) => <li key={error}>{error}</li>)}</ul>
        </div>
      )}

      <div className="variant-intent-confirmation">
        <Checkbox
          id="variant-intent-confirmation"
          checked={intentConfirmed}
          onCheckedChange={(checked) => setIntentConfirmed(checked === true)}
        />
        <label htmlFor="variant-intent-confirmation">
          {l("I confirm that these selections exactly reflect my order intent. The actual combination, inventory, and price may not yet be verified.", "이 선택이 내가 원하는 정확한 주문 의도임을 확인합니다. 실제 조합·재고·가격은 아직 검증되지 않을 수 있습니다.")}
        </label>
      </div>

      <footer>
        <Button emphasis="quiet" onClick={onCancel}>
          {l("Back", "돌아가기")}
        </Button>
        <Button
          type="submit"
          emphasis="primary"
          busy={working}
          disabled={!canConfirm}
        >
          {working ? resolvedWorkingLabel : resolvedConfirmLabel}
        </Button>
      </footer>
      <small className="variant-external-effect">
        {l("MANUAL_MERCHANT_ORDER · externalEffect=SIMULATED · No external order is created", "MANUAL_MERCHANT_ORDER · externalEffect=SIMULATED · 외부 주문은 생성되지 않음")}
      </small>
    </form>
  );
}

type FieldEditorProps = {
  field: VariantField;
  index: number;
  value: string;
  onFieldChange: (patch: Partial<VariantField>) => void;
  onKeyChange: (key: string) => void;
  onValueChange: (value: string) => void;
  onRemove: () => void;
};

function VariantFieldEditor({
  field,
  index,
  value,
  onFieldChange,
  onKeyChange,
  onValueChange,
  onRemove,
}: FieldEditorProps) {
  const { l } = useLocale();
  return (
    <fieldset className="variant-field">
      <legend>
        <span>{String(index + 1).padStart(2, "0")}</span>
        {field.source === "USER" ? l("User field", "사용자 field") : l("Agent-observed field", "Agent 관찰 field")}
      </legend>
      <div className="variant-field-definition">
        <label>
          {l("Display name", "표시 이름")}
          <Input
            aria-label={l("Display name for option {number}", "옵션 {number} 표시 이름", { number: index + 1 })}
            value={field.label}
            maxLength={80}
            onChange={(event) => onFieldChange({ label: event.target.value })}
          />
        </label>
        <label>
          {l("Storage key", "저장 key")}
          <Input
            aria-label={l("Storage key for option {number}", "옵션 {number} 저장 key", { number: index + 1 })}
            value={field.key}
            maxLength={64}
            onChange={(event) => onKeyChange(event.target.value.toLowerCase())}
          />
        </label>
        <label>
          {l("Input method", "입력 방식")}
          <NativeSelect
            aria-label={l("Input method for {field}", "{field} 입력 방식", { field: field.label })}
            value={field.inputKind}
            onChange={(event) =>
              onFieldChange({ inputKind: event.target.value as VariantInputKind })
            }
          >
            <NativeSelectOption value="ENUM">{l("Known values only", "알려진 값만")}</NativeSelectOption>
            <NativeSelectOption value="ENUM_OR_VALUE">{l("Known value or custom input", "알려진 값 또는 직접 입력")}</NativeSelectOption>
            <NativeSelectOption value="VALUE">{l("Custom input", "직접 입력")}</NativeSelectOption>
          </NativeSelect>
        </label>
      </div>

      {field.inputKind !== "VALUE" && (
        <label className="variant-known-values">
          {l("Known values", "알려진 값")} <small>{l("Edit as a comma-separated list.", "쉼표로 구분해 수정할 수 있습니다.")}</small>
          <Input
            aria-label={l("Known values for {field}", "{field} 알려진 값", { field: field.label })}
            value={field.knownValues.map((item) => item.label).join(", ")}
            onChange={(event) =>
              onFieldChange({
                knownValues: event.target.value
                  .split(",")
                  .map((item) => item.trim())
                  .filter(Boolean)
                  .map((item) => ({ value: item, label: item })),
              })
            }
          />
        </label>
      )}

      <div className="variant-selection-row">
        <label>
          {l("My selection", "내 선택")} {field.required && <em>{l("Required", "필수")}</em>}
          {field.inputKind === "ENUM" ? (
            <NativeSelect
              aria-label={l("Select {field}", "{field} 선택", { field: field.label })}
              value={value}
              required={field.required}
              onChange={(event) => onValueChange(event.target.value)}
            >
              <NativeSelectOption value="">{l("Select", "선택하세요")}</NativeSelectOption>
              {field.knownValues.map((item) => (
                <NativeSelectOption key={item.value} value={item.value}>{item.label}</NativeSelectOption>
              ))}
            </NativeSelect>
          ) : (
            <>
              <Input
                aria-label={l("Select {field}", "{field} 선택", { field: field.label })}
                list={field.inputKind === "ENUM_OR_VALUE" ? `variant-values-${index}` : undefined}
                value={value}
                required={field.required}
                placeholder={field.inputKind === "VALUE" ? l("Example: 220V", "예: 220V") : l("Choose from the list or enter a value", "목록에서 선택하거나 직접 입력")}
                onChange={(event) => onValueChange(event.target.value)}
              />
              {field.inputKind === "ENUM_OR_VALUE" && (
                <datalist id={`variant-values-${index}`}>
                  {field.knownValues.map((item) => (
                    <NativeSelectOption key={item.value} value={item.value}>{item.label}</NativeSelectOption>
                  ))}
                </datalist>
              )}
            </>
          )}
        </label>
        <div className="variant-required-toggle">
          <Switch
            id={`variant-required-${index}`}
            checked={field.required}
            onCheckedChange={(checked) => onFieldChange({ required: checked })}
          />
          <label htmlFor={`variant-required-${index}`}>{l("Required option", "필수 옵션")}</label>
        </div>
        <Button emphasis="quiet" size="compact" onClick={onRemove}>
          {l("Remove option", "옵션 제거")}
        </Button>
      </div>
    </fieldset>
  );
}

function validateConfiguration(
  fields: VariantField[],
  selections: Record<string, string>,
  confirmsNoOptions: boolean,
  l: Localize,
) {
  if (confirmsNoOptions) return [];
  const errors: string[] = [];
  if (fields.length === 0) {
    errors.push(l("Add an option field or confirm 'No options'.", "옵션 field를 추가하거나 ‘옵션 없음’을 확인하세요."));
    return errors;
  }
  const keys = new Set<string>();
  fields.forEach((field, index) => {
    const fieldName = field.label.trim() || l("Option {number}", "옵션 {number}", { number: index + 1 });
    if (!field.label.trim()) errors.push(l("Enter the display name for option {number}.", "옵션 {number}의 표시 이름을 입력하세요.", { number: index + 1 }));
    const canonicalKey = field.key.trim().toLocaleLowerCase();
    if (!canonicalKey || canonicalKey.length > 64) {
      errors.push(l("Enter a storage key of 64 characters or fewer for {field}.", "{field}의 저장 key를 64자 이내로 입력하세요.", { field: fieldName }));
    } else if (keys.has(canonicalKey)) {
      errors.push(l("The storage key {key} is duplicated.", "{key} 저장 key가 중복됩니다.", { key: canonicalKey }));
    }
    keys.add(canonicalKey);
    if (field.inputKind === "ENUM" && field.knownValues.length === 0) {
      errors.push(l("Enter at least one known selectable value for {field}.", "{field}에 선택 가능한 알려진 값을 하나 이상 입력하세요.", { field: fieldName }));
    }
    const selection = selections[field.key]?.trim() ?? "";
    if (field.required && !selection) errors.push(l("Select or enter a value for {field}.", "{field} 값을 선택하거나 입력하세요.", { field: fieldName }));
    if (
      selection &&
      field.inputKind === "ENUM" &&
      !field.knownValues.some((item) => item.value === selection)
    ) {
      errors.push(l("Choose {field} from the known values.", "{field}은 알려진 값 중에서 선택하세요.", { field: fieldName }));
    }
  });
  return errors;
}
