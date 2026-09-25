import { describe, expect, it } from 'vitest';
import { DAY, initialState, reducer, visibleCandidates, type Mission } from './model';

const now = 1_800_000_000_000;
function mission(id: string, price = 300): Mission {
  return { id, targetId: 'pens', kind: 'price', productId: 'pilot', price, axes: { writing: 1, design: 2, value: 5, durability: 3 }, expiresAt: now + 7 * DAY, state: 'ACTIVE' };
}
describe('Curation studio is a bounded, synthetic interaction model', () => {
  it('sorts by distinct meanings without changing candidate data', () => {
    const target = initialState(now).targets[0];
    expect(visibleCandidates(target)[0].id).toBe('pilot');
    expect(visibleCandidates({ ...target, sort: 'price' })[0].id).toBe('lamy');
    expect(visibleCandidates(target).map(c => c.score)).toEqual([94, 89, 87]);
    expect(target.candidates[0].id).toBe('pilot');
  });
  it('separates hidden from hard filters and restores candidates without deletion', () => {
    const target = initialState(now).targets[0];
    expect(visibleCandidates({ ...target, maxPrice: 250 }).map(c => c.id)).toEqual(['lamy']);
    expect(visibleCandidates({ ...target, showHidden: true, maxPrice: 250 }).map(c => c.id)).toEqual(['lamy', 'custom74']);
    expect(visibleCandidates({ ...target, minScore: 90 }).map(c => c.id)).toEqual(['pilot']);
    expect(visibleCandidates({ ...target, minPrice: 250, maxPrice: 400 }).map(c => c.id)).toEqual(['pilot']);
    expect(visibleCandidates({ ...target, minPrice: 500, maxPrice: 200 })).toEqual([]);
    expect(target.candidates).toHaveLength(4);
  });
  it('changes priorities without rewriting old explanations or active mission snapshots', () => {
    const state = initialState(now);
    const next = reducer(state, { type: 'axis', id: 'pens', key: 'writing', value: 1 });
    expect(next.targets[0].axes.writing).toBe(1);
    expect(next.targets[0].candidates).toEqual(state.targets[0].candidates);
    expect(next.missions).toEqual(state.missions);
  });
  it('admits only one foreground action and cancellation prevents late completion', () => {
    const state = initialState(now);
    const started = reducer(state, { type: 'start', id: 'one', targetId: 'pens', kind: 'research' });
    expect(reducer(started, { type: 'start', id: 'two', targetId: 'pens', kind: 'expand' }).instant?.id).toBe('one');
    const cancelled = reducer(started, { type: 'cancel' });
    const late = reducer(cancelled, { type: 'tick', now: now + DAY });
    expect(late.instant).toBeNull();
    expect(late.targets[0].candidates).toEqual(state.targets[0].candidates);
    expect(late.missions.filter(m => m.state === 'ACTIVE')).toHaveLength(2);
  });
  it('append deduplicates identity and leaves a newly found catalog candidate unassessed', () => {
    let state = initialState(now);
    const before = structuredClone(state.targets[0].candidates);
    state = reducer(state, { type: 'start', id: 'one', targetId: 'pens', kind: 'expand' });
    state = reducer(state, { type: 'tick', now: now + 5000 });
    expect(visibleCandidates(state.targets[0])).toHaveLength(4);
    expect(state.targets[0].candidates.slice(0, 3)).toEqual(before.slice(0, 3));
    expect(state.targets[0].candidates[3]).toMatchObject({ generation: 0, score: null });
    expect(visibleCandidates({ ...state.targets[0], minScore: 1 })).toHaveLength(3);
    expect(new Set(state.targets[0].candidates.map(c => c.id)).size).toBe(4);
  });
  it('pins survive replacement and a running research uses its start snapshot', () => {
    let state = initialState(now);
    state = reducer(state, { type: 'candidate', targetId: 'pens', id: 'pelikan', field: 'pinned' });
    state = reducer(state, { type: 'start', id: 'one', targetId: 'pens', kind: 'research' });
    state = reducer(state, { type: 'axis', id: 'pens', key: 'writing', value: 1 });
    state = reducer(state, { type: 'tick', now: now + 5000 });
    const target = state.targets[0];
    expect(target.candidates.find(c => c.id === 'pelikan')).toMatchObject({ pinned: true, hidden: false, generation: 0 });
    expect(target.candidates.find(c => c.id === 'lamy')).toMatchObject({ axis: 'writing', generation: 1 });
    expect(target.criteriaChanged).toBe(true);
  });
  it('limits ongoing research to 10 for the fixture account', () => {
    let state = initialState(now);
    for (let i = 0; i < 12; i++) state = reducer(state, { type: 'create', mission: mission(`m${i}`, 300 + i) });
    expect(state.missions.filter(m => m.state === 'ACTIVE')).toHaveLength(10);
    expect(state.message).toBe('limit');
  });
  it('does not create duplicates and a terminal task releases its slot', () => {
    let state = reducer(initialState(now), { type: 'create', mission: mission('a') });
    state = reducer(state, { type: 'create', mission: mission('b') });
    expect(state.missions).toHaveLength(3);
    expect(state.message).toBe('duplicate');
    state = reducer(state, { type: 'end', id: 'a', outcome: 'CANCELLED' });
    state = reducer(state, { type: 'create', mission: mission('c') });
    expect(state.missions).toHaveLength(4);
    expect(state.missions.find(m => m.id === 'a')?.outcome).toBe('CANCELLED');
  });
  it('requires a bounded future expiry and valid price', () => {
    const state = initialState(now);
    for (const m of [{ ...mission('a'), expiresAt: now }, { ...mission('a'), expiresAt: now + 31 * DAY }, { ...mission('a'), price: NaN }, { ...mission('a'), minPrice: 301 }, { ...mission('a'), minPrice: -1 }]) {
      expect(reducer(state, { type: 'create', mission: m })).toEqual(state);
    }
  });
  it('preserves a valid lower bound and compares both bounds for duplicates', () => {
    let state = reducer(initialState(now), { type: 'create', mission: { ...mission('range'), minPrice: 200 } });
    expect(state.missions.at(-1)?.minPrice).toBe(200);
    state = reducer(state, { type: 'create', mission: { ...mission('other-range'), minPrice: 250 } });
    expect(state.missions).toHaveLength(4);
  });
  it('expires automatically and prevents a late result or extension from reopening it', () => {
    const expired = reducer(initialState(now), { type: 'tick', now: now + 8 * DAY });
    expect(expired.missions[0]).toMatchObject({ state: 'ENDED', outcome: 'EXPIRED' });
    const late = reducer(expired, { type: 'end', id: 'watch-lamy', outcome: 'FOUND' });
    expect(late.missions[0].outcome).toBe('EXPIRED');
    expect(reducer(late, { type: 'extend', id: 'watch-lamy' }).missions[0]).toEqual(late.missions[0]);
  });
  it('extends a task while respecting the 30-day horizon', () => {
    let state = initialState(now);
    for (let i = 0; i < 10; i++) state = reducer(state, { type: 'extend', id: 'watch-lamy' });
    expect(state.missions[0].expiresAt).toBe(now + 30 * DAY);
  });
  it('records a sample result without overwriting the candidate price or creating a purchase', () => {
    const before = reducer(initialState(now), { type: 'create', mission: mission('result') });
    const after = reducer(before, { type: 'end', id: 'result', outcome: 'FOUND' });
    expect(after.missions.at(-1)?.result).toEqual({ productId: 'pilot', price: 300, observedAt: now });
    expect(after.targets).toEqual(before.targets);
    expect(after.cart).toEqual([]);
  });
  it('delivers one reminder at its time without requiring a price or making a catalog claim', () => {
    const reminder: Mission = { ...mission('reminder'), kind: 'reminder', price: undefined, productId: undefined, remindAt: now + DAY, expiresAt: now + DAY };
    const created = reducer(initialState(now), { type: 'create', mission: reminder });
    expect(created.missions.at(-1)?.state).toBe('ACTIVE');
    expect(reducer(created, { type: 'tick', now: now + DAY - 1 }).missions.at(-1)?.state).toBe('ACTIVE');
    const arrived = reducer(created, { type: 'tick', now: now + DAY });
    expect(arrived.missions.at(-1)).toMatchObject({ state: 'ENDED', outcome: 'FOUND', result: { observedAt: now + DAY } });
    expect(arrived.missions.at(-1)?.result?.price).toBeUndefined();
    expect(reducer(arrived, { type: 'tick', now: now + 2 * DAY }).missions).toEqual(arrived.missions);
    expect(arrived.targets).toEqual(created.targets);
  });
  it('rejects invalid reminder time/price conditions and compares reminder dates for duplicates', () => {
    const reminder: Mission = { ...mission('reminder'), kind: 'reminder', price: undefined, remindAt: now + DAY, expiresAt: now + DAY };
    const initial = initialState(now);
    for (const m of [{ ...reminder, remindAt: undefined }, { ...reminder, remindAt: now }, { ...reminder, expiresAt: NaN }, { ...reminder, price: 300 }]) expect(reducer(initial, { type: 'create', mission: m })).toEqual(initial);
    let state = reducer(initial, { type: 'create', mission: reminder });
    expect(reducer(state, { type: 'create', mission: { ...reminder, id: 'duplicate' } }).message).toBe('duplicate');
    state = reducer(state, { type: 'create', mission: { ...reminder, id: 'later', remindAt: now + 2 * DAY, expiresAt: now + 2 * DAY } });
    expect(state.missions).toHaveLength(4);
  });
  it('moves a reminder time with extension and keeps cancelled reminders silent', () => {
    const reminder: Mission = { ...mission('reminder'), kind: 'reminder', price: undefined, remindAt: now + DAY, expiresAt: now + DAY };
    let state = reducer(initialState(now), { type: 'create', mission: reminder });
    state = reducer(state, { type: 'extend', id: 'reminder' });
    expect(state.missions.at(-1)).toMatchObject({ remindAt: now + 8 * DAY, expiresAt: now + 8 * DAY });
    state = reducer(state, { type: 'end', id: 'reminder', outcome: 'CANCELLED' });
    state = reducer(state, { type: 'tick', now: now + 9 * DAY });
    expect(state.missions.at(-1)).toMatchObject({ state: 'ENDED', outcome: 'CANCELLED' });
    expect(state.missions.at(-1)?.result).toBeUndefined();
  });
});
