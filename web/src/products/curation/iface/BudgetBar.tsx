import { useCurationThreads } from "../app/useThreads";
import { useRef, useState, type ReactNode } from "react";
import { ChevronRight, Wallet } from "lucide-react";
import { APIError } from "../../../shared/api/client";
import { randomUUID } from "../../../shared/browser/randomUUID";
import { useLocale } from "../../../shared/i18n";
import { Button, Input, NativeSelect, NativeSelectOption, Popover, PopoverAnchor, PopoverArrow, PopoverContent } from "../../../shared/ui";
import { useBudget } from "../app/useBudget";
import { allocateBudget, allocationTotal, budgetAmount, budgetMinor, budgetSchema, type BudgetCommand, type BudgetCurrency, type BudgetLedger, type TargetBudget } from "../domain/budget";
import { useResearchCurrency } from "../research/app/useResearchCurrency";
import { convertResearchMinor } from "../research/domain/exchangeRate";
import { useBudgetMoney } from "./BudgetViews";
import "./budget.css";

export function BudgetBar({ settings, action }: { settings?: ReactNode; action?: ReactNode }) {
  const threads = useCurationThreads();
  const { ledger, targets, error, reload } = useBudget(); const { l } = useLocale(); const money = useBudgetMoney(true);
  const [editing, setEditing] = useState<string>();
  const dismissLocked = useRef(false); const trigger = useRef<HTMLButtonElement | null>(null);
  const popoverAnchor = useRef({
    getBoundingClientRect: () => trigger.current?.getBoundingClientRect() ?? new DOMRect(),
  });
  const total = budgetMinor(ledger?.totalAmount, ledger?.currency ?? "KRW");
  function edit(id: string, button: HTMLButtonElement) { if (!dismissLocked.current && !threads?.busy) { trigger.current = button; setEditing(id); } }
  return <div className="curation-budget" data-testid="curation-budget">
    <div className="curation-budget__toolbar">{settings}{action}</div>
    <Popover open={Boolean(editing && ledger)} onOpenChange={open => { if (!open && !dismissLocked.current) setEditing(undefined); }}>
      <PopoverAnchor virtualRef={popoverAnchor} />
      <div className="curation-budget__allocation-row">
        <Button type="button" emphasis="quiet" size="compact" className="curation-budget__total-trigger" aria-pressed={ledger?.enabled ?? false} aria-haspopup="dialog" onClick={e => edit("total", e.currentTarget)} disabled={!ledger || Boolean(threads?.busy)} aria-label={l("Budget settings", "예산 설정")}>
          <Wallet size={14} aria-hidden="true" /><span>{l("Budget", "예산")} · {ledger ? ledger.enabled && total !== undefined ? money(total, ledger.currency) : l("Not set", "설정 안됨") : l("Loading", "불러오는 중")}</span><ChevronRight size={13} aria-hidden="true" />
        </Button>
        <div className={`curation-budget__bar ${ledger?.enabled ? "" : "is-unlimited"}`} aria-label={l("Target budget allocation", "Target 예산 배분")}>
          {targets.length ? targets.map(target => {
            const allocation = ledger?.allocations.find(a => a.targetId === target.id); const amount = budgetMinor(allocation?.amount, ledger?.currency ?? "KRW");
            const label = ledger?.enabled && amount !== undefined ? money(amount, ledger.currency) : l("No limit", "제한없음");
            const targetLabel = `${target.title} ×${allocation?.quantity ?? 1} · ${label}`;
            const segmentStyle = { flexGrow: total && amount ? Number(amount * 10000n / total) : ledger?.enabled && total ? 0 : 1 };
            return <Button type="button" key={target.id} emphasis="quiet" className="curation-budget__segment" style={segmentStyle} disabled={!ledger?.enabled || !allocation || Boolean(threads?.busy)} onClick={e => edit(target.id, e.currentTarget)} title={targetLabel} aria-label={targetLabel}><span>{label}</span></Button>;
          }) : <Button type="button" emphasis="quiet" className="curation-budget__segment" disabled={!ledger?.enabled} onClick={e => edit("total", e.currentTarget)}><span>{ledger?.enabled && total !== undefined ? money(total, ledger.currency) : l("No limit", "제한없음")}</span></Button>}
        </div>
      </div>
      {editing && ledger && <PopoverContent side="top" align="center" sideOffset={10} collisionPadding={12} className="budget-popover" aria-label={l("Budget settings", "예산 설정")}
        onInteractOutside={e => { if (dismissLocked.current) e.preventDefault(); }} onEscapeKeyDown={e => { if (dismissLocked.current) e.preventDefault(); }}
        onCloseAutoFocus={e => { e.preventDefault(); trigger.current?.focus(); }}>
        <BudgetEditor key={editing} initial={ledger} targetId={editing === "total" ? undefined : editing} onClose={() => setEditing(undefined)} lockDismiss={locked => { dismissLocked.current = locked; }} />
        <PopoverArrow width={14} height={7} className="budget-popover__arrow" />
      </PopoverContent>}
    </Popover>
    {error && <Button type="button" emphasis="quiet" size="compact" onClick={() => void reload()}>{l("Reload budget", "예산 다시 불러오기")}</Button>}
  </div>;
}

function BudgetEditor({ initial, targetId, onClose, lockDismiss }: { initial: BudgetLedger; targetId?: string; onClose: () => void; lockDismiss: (locked: boolean) => void }) {
  const { l, locale } = useLocale(); const { targets, save } = useBudget(); const { currency: displayCurrency, exchange } = useResearchCurrency();
  function convert(value: string | null, from: string, to: BudgetCurrency): string | null | undefined {
    if (value === null) return null;
    const minor = budgetMinor(value, from); if (minor === undefined) return undefined;
    if (from === to) return value;
    const result = exchange?.rate ? convertResearchMinor(Number(minor), from, to, exchange.rate) : undefined;
    return result === undefined ? undefined : budgetAmount(BigInt(result), to);
  }
  function convertRows(rows: TargetBudget[], from: string, to: BudgetCurrency): TargetBudget[] | undefined {
    const result: TargetBudget[] = [];
    for (const row of rows) { const amount = convert(row.amount, from, to); if (amount === undefined) return undefined; result.push({ ...row, amount, minimumUnitAmount: from === to ? row.minimumUnitAmount : undefined }); }
    return result;
  }
  const [seed] = useState(() => { const converted = convertRows(initial.allocations, initial.currency, displayCurrency); return { currency: converted ? displayCurrency : initial.currency, rows: converted ?? initial.allocations }; });
  const [currency, setCurrency] = useState<BudgetCurrency>(seed.currency);
  const [allocations, setAllocations] = useState<TargetBudget[]>(seed.rows);
  const target = allocations.find(a => a.targetId === targetId);
  const [step, setStep] = useState<"AMOUNT" | "ALLOCATION" | "TARGET" | "DISABLE">(target ? "TARGET" : initial.enabled ? "DISABLE" : "AMOUNT");
  const [total, setTotal] = useState(initial.enabled ? budgetAmount(allocationTotal(seed.rows, seed.currency) ?? 0n, seed.currency) : "");
  const mode = "MANUAL";
  const [busy, setBusy] = useState(false); const [error, setError] = useState<string>(); const [uncertain, setUncertain] = useState(false);
  const pending = useRef<BudgetCommand | undefined>(undefined); const generation = useRef(0);
  const sum = allocationTotal(allocations, currency); const totalMinor = budgetMinor(total, currency);
  const originalRows = convertRows(initial.allocations, initial.currency, currency);
  const originalSum = originalRows ? allocationTotal(originalRows, currency) : undefined;
  const delta = sum !== undefined && originalSum !== undefined ? sum - originalSum : undefined;
  const quantitiesValid = allocations.every(a => Number.isInteger(a.quantity) && a.quantity >= 1 && a.quantity <= 99);
  const targetValid = Boolean(target && quantitiesValid && (!initial.enabled || sum !== undefined));
  const allocationValid = quantitiesValid && totalMinor !== undefined && totalMinor === sum && (allocations.length > 0 || totalMinor === 0n);
  const disabled = busy || uncertain;
  const money = (value: bigint) => new Intl.NumberFormat(locale, { style: "currency", currency, maximumFractionDigits: currency === "KRW" ? 0 : 2 }).format(Number(value) / (currency === "KRW" ? 1 : 100));

  function changeCurrency(next: BudgetCurrency) {
    const rows = convertRows(allocations, currency, next); const nextTotal = total ? convert(total, currency, next) : "";
    if (!rows || nextTotal === undefined) { setError(l("The daily exchange rate is unavailable. Try changing currency again shortly.", "일일 환율을 확인할 수 없습니다. 잠시 후 통화를 다시 변경해 주세요.")); return; }
    const matched = totalMinor !== undefined && sum === totalMinor;
    setAllocations(rows); setTotal(matched ? budgetAmount(allocationTotal(rows, next) ?? 0n, next) : nextTotal ?? ""); setCurrency(next); setError(undefined);
  }
  function updateRow(id: string, changes: Partial<TargetBudget>) { setAllocations(rows => rows.map(row => row.targetId === id ? { ...row, ...changes } : row)); }
  async function commit(fields: Omit<BudgetCommand, "schemaVersion" | "commandId" | "expectedVersion">) {
    const command = pending.current ?? { ...fields, schemaVersion: budgetSchema, commandId: randomUUID(), expectedVersion: initial.version };
    pending.current = command; lockDismiss(true); setBusy(true); setError(undefined);
    try { await save(command); pending.current = undefined; lockDismiss(false); onClose(); }
    catch (caught) {
      const known = caught instanceof APIError && caught.status >= 400 && caught.status < 500;
      if (known) { pending.current = undefined; lockDismiss(false); }
      setUncertain(!known);
      setError(!known ? l("Save unconfirmed. Retry to check the same change.", "저장 결과를 확인하지 못했습니다. 다시 확인해 주세요.") : caught instanceof APIError && caught.code === "BUDGET_VERSION_CONFLICT" ? l("The budget changed. Reopen to edit the latest values.", "예산이 변경되었습니다. 다시 열어 최신 값으로 편집해 주세요.") : l("Check the amounts and quantities. Nothing was saved.", "금액과 수량을 확인해 주세요. 저장되지 않았습니다."));
    } finally { setBusy(false); }
  }
  function distribute() {
    if (totalMinor === undefined) return;
    const amounts = allocateBudget(totalMinor, initial.allocations.map(() => 1n));
    setAllocations(rows => rows.map((a, i) => ({ ...a, amount: budgetAmount(amounts[i], currency) })));
  }
  function close() { if (!pending.current) { generation.current++; onClose(); } }
  function saveDraft() {
    if (step === "TARGET" && target) {
      if (!initial.enabled) { void commit({ kind: "SET_QUANTITY", targetId, quantity: target.quantity }); return; }
      if (currency === initial.currency) { void commit({ kind: "SET_TARGET", targetId, currency, amount: target.amount ?? undefined, quantity: target.quantity, updateMinimum: true }); return; }
    }
    const rows = allocations.map(a => { const limit = budgetMinor(a.amount, currency); const floor = budgetMinor(a.minimumUnitAmount, currency); return { ...a, minimumUnitAmount: limit !== undefined && floor !== undefined && floor * BigInt(a.quantity) <= limit ? a.minimumUnitAmount : undefined }; });
    void commit({ kind: initial.enabled ? "SET_TOTAL" : "ENABLE", currency, totalAmount: step === "TARGET" ? budgetAmount(sum!, currency) : total, allocationMode: "MANUAL", allocations: rows, updateMinimum: true });
  }
  function currencySelect(label: string) { return <NativeSelect aria-label={label} value={currency} disabled={disabled} onChange={e => changeCurrency(e.target.value as BudgetCurrency)}><NativeSelectOption value="KRW">{l("KRW", "KRW")}</NativeSelectOption><NativeSelectOption value="USD">{l("USD", "USD")}</NativeSelectOption></NativeSelect>; }
  function targetRow(row: TargetBudget) {
    const title = targets.find(t => t.id === row.targetId)?.title ?? row.targetId;
    return <div className="budget-editor__row" key={row.targetId}>
      <span className="budget-editor__target" title={title}>{title}</span>
      <Input aria-label={l("Budget for {target}", "{target} 배분액", { target: title })} inputMode="decimal" value={row.amount ?? ""} placeholder={l("No limit", "제한없음")} disabled={disabled || (step === "TARGET" ? !initial.enabled : mode !== "MANUAL")} onChange={e => updateRow(row.targetId, { amount: e.target.value })} />
      {currencySelect(l("Budget currency for {target}", "{target} 예산 통화", { target: title }))}
      <div className="budget-editor__quantity"><span aria-hidden="true">×</span><Input aria-label={l("Goal quantity for {target}", "{target} 목표 수량", { target: title })} type="number" min="1" max="99" step="1" value={Number.isNaN(row.quantity) ? "" : row.quantity} disabled={disabled} onChange={e => updateRow(row.targetId, { quantity: Number(e.target.value) })} /></div>
    </div>;
  }
  return <div className="budget-editor">
    <div className="budget-editor__heading"><strong>{step === "DISABLE" ? l("Turn off budget settings?", "예산 설정을 해제하시겠습니까?") : l("Budget", "예산")}</strong></div>
    {step === "DISABLE" ? null : step === "AMOUNT" ? <div className="budget-editor__amount-row"><label htmlFor="budget-total">{l("Total", "총액")}</label><Input id="budget-total" autoFocus aria-label={l("Total budget", "총 예산")} inputMode="decimal" value={total} disabled={disabled} onChange={e => setTotal(e.target.value)} placeholder={l("Enter amount", "금액 입력")} />{currencySelect(l("Budget currency", "예산 통화"))}</div> : <>

      <div className="budget-editor__rows">{(step === "TARGET" && target ? [target] : allocations).map(targetRow)}</div>
      {initial.enabled || step === "ALLOCATION" ? <p className="budget-editor__summary" role="status">{l("Total: {amount}", "총액: {amount}", { amount: sum === undefined ? "—" : money(sum) })}{step === "ALLOCATION" && !allocationValid ? ` · ${l("Allocate exactly {amount}", "{amount}에 맞춰 배분해 주세요", { amount: totalMinor === undefined ? "—" : money(totalMinor) })}` : ""}</p> : <Button type="button" size="compact" emphasis="quiet" disabled={disabled} onClick={() => setStep("AMOUNT")}>{l("Add budget", "예산 추가")}</Button>}
    </>}
    {step === "TARGET" && currency !== initial.currency && initial.enabled && <p className="budget-editor__summary">{l("{from} → {to}: all allocations use the daily rate when saved.", "{from} → {to}: 저장 시 전체 배분을 일일 환율로 환산합니다.", { from: initial.currency, to: currency })}</p>}
    {step === "TARGET" && delta !== undefined && delta !== 0n && <p className="budget-editor__summary" role="status">{delta > 0n ? l("Total increases by {amount}", "총액 {amount} 증가", { amount: money(delta) }) : l("Total decreases by {amount}", "총액 {amount} 감소", { amount: money(-delta) })}</p>}
    {busy && !pending.current && <p className="budget-editor__summary" role="status">{l("Allocating…", "배분 중…")}</p>}
    {error && <p className="budget-editor__error" role="alert">{error}</p>}
    <footer className="budget-editor__footer"><Button type="button" size="compact" emphasis="quiet" disabled={Boolean(pending.current)} onClick={close}>{l("Cancel", "취소")}</Button>
      {step === "ALLOCATION" && <Button type="button" size="compact" emphasis="quiet" disabled={disabled} onClick={() => { generation.current++; setStep("AMOUNT"); }}>{l("Back", "이전")}</Button>}
      {uncertain ? <Button type="button" size="compact" emphasis="primary" disabled={busy} onClick={() => pending.current && void commit(pending.current)}>{l("Retry save", "저장 다시 확인")}</Button> : step === "DISABLE" ? <Button type="button" size="compact" emphasis="primary" disabled={busy} onClick={() => void commit({ kind: "DISABLE" })}>{l("Confirm", "확인")}</Button> : step === "AMOUNT" ? <Button type="button" size="compact" emphasis="primary" disabled={busy || totalMinor === undefined || (!allocations.length && totalMinor !== 0n)} onClick={() => { setStep("ALLOCATION"); distribute(); }}>{l("Allocate", "배분하기")}</Button> : <Button type="button" size="compact" emphasis="primary" disabled={busy || (step === "TARGET" ? !targetValid : !allocationValid)} onClick={saveDraft}>{l("Save", "저장")}</Button>}
    </footer>
  </div>;
}
