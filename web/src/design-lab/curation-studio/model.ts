// Synthetic UI exploration only. No API, durable scheduler or commerce authority.
export type AxisKey = 'writing' | 'design' | 'value' | 'durability';
export type Axes = Record<AxisKey, number>;
export type Sort = 'pick' | 'price';
export type MissionKind = 'price' | 'discover' | 'deal' | 'reminder';
export type Outcome = 'FOUND' | 'FAILED' | 'CANCELLED' | 'EXPIRED';
export interface Mission {
  id: string;
  targetId: string;
  kind: MissionKind;
  productId?: string;
  price?: number;
  minPrice?: number;
  remindAt?: number;
  expiresAt: number;
  axes: Axes;
  state: 'ACTIVE' | 'ENDED';
  outcome?: Outcome;
  result?: { productId?: string; price?: number; observedAt: number };
}
export interface Candidate {
  id: string;
  name: string;
  image: 'pilot' | 'lamy' | 'pelikan' | 'custom74';
  price: number;
  score: number | null;
  axis: AxisKey;
  pinned: boolean;
  hidden: boolean;
  generation: number;
}
export interface Target {
  id: string;
  title: string;
  axes: Axes;
  candidates: Candidate[];
  sort: Sort;
  minPrice: number | null;
  maxPrice: number | null;
  minScore: number | null;
  collapsed: boolean;
  showHidden: boolean;
  criteriaChanged: boolean;
}
export interface StudioState {
  targets: Target[];
  missions: Mission[];
  cart: string[];
  instant: null | { id: string; targetId: string; kind: 'expand' | 'research'; axes: Axes; dueAt: number };
  now: number;
  message: 'added' | 'ended' | 'extended' | 'duplicate' | 'limit' | 'research' | 'cancelled' | null;
}
export const DAY = 86400000;
export const seedAxes: Axes = { writing: 5, design: 4, value: 3, durability: 2 };
const candidates: Candidate[] = [
  { id: 'pilot', name: 'Amber Writer', image: 'pilot', price: 336, score: 94, axis: 'writing', pinned: false, hidden: false, generation: 0 },
  { id: 'lamy', name: 'Classic Black', image: 'lamy', price: 223, score: 89, axis: 'design', pinned: false, hidden: false, generation: 0 },
  { id: 'pelikan', name: 'Evergreen 800', image: 'pelikan', price: 560, score: 87, axis: 'durability', pinned: false, hidden: false, generation: 0 },
  { id: 'custom74', name: 'Smoke Writer', image: 'custom74', price: 176, score: 84, axis: 'value', pinned: false, hidden: true, generation: 0 },
];
export function initialState(now = Date.now()): StudioState {
  return {
    now, cart: [], instant: null, message: null,
    targets: [{ id: 'pens', title: '오래 쓸 만년필', axes: { ...seedAxes }, candidates: structuredClone(candidates), sort: 'pick', minPrice: null, maxPrice: null, minScore: null, collapsed: false, showHidden: false, criteriaChanged: false }],
    missions: [
      { id: 'watch-lamy', targetId: 'pens', kind: 'price', productId: 'lamy', price: 200, expiresAt: now + 7 * DAY, axes: { ...seedAxes }, state: 'ACTIVE' },
      { id: 'discover-pens', targetId: 'pens', kind: 'discover', price: 600, expiresAt: now + 14 * DAY, axes: { ...seedAxes }, state: 'ACTIVE' },
    ],
  };
}
export type Action =
  | { type: 'target'; id: string; patch: Partial<Pick<Target, 'sort' | 'minPrice' | 'maxPrice' | 'minScore' | 'collapsed' | 'showHidden'>> }
  | { type: 'axis'; id: string; key: AxisKey; value: number }
  | { type: 'candidate'; targetId: string; id: string; field: 'pinned' | 'hidden' }
  | { type: 'cart'; id: string }
  | { type: 'start'; id: string; targetId: string; kind: 'expand' | 'research' }
  | { type: 'cancel' }
  | { type: 'tick'; now: number }
  | { type: 'create'; mission: Mission }
  | { type: 'end'; id: string; outcome: Outcome }
  | { type: 'extend'; id: string }
  | { type: 'reset'; now: number }
  | { type: 'clear-message' };

function mapTarget(state: StudioState, id: string, update: (target: Target) => Target) {
  return { ...state, targets: state.targets.map(target => target.id === id ? update(target) : target) };
}
function finishMission(state: StudioState, mission: Mission, outcome: Outcome): Mission {
  if (mission.state !== 'ACTIVE') return mission;
  const candidate = state.targets.find(t => t.id === mission.targetId)?.candidates.find(c => c.id === mission.productId);
  // Every result here is synthetic; it is never evidence of a merchant price.
  return { ...mission, state: 'ENDED', outcome, ...(outcome === 'FOUND' ? { result: {
    observedAt: state.now,
    ...(candidate && mission.kind !== 'reminder' ? { productId: candidate.id, price: mission.price } : {}),
  } } : {}) };
}
export function visibleCandidates(target: Target) {
  return target.candidates.filter(candidate => (target.showHidden || !candidate.hidden)
    && (target.minPrice === null || candidate.price >= target.minPrice)
    && (target.maxPrice === null || candidate.price <= target.maxPrice)
    && (target.minScore === null || (candidate.score !== null && candidate.score >= target.minScore)))
    .sort((a, b) => (target.sort === 'price' ? a.price - b.price : (b.score ?? -1) - (a.score ?? -1)) || a.id.localeCompare(b.id));
}
export function reducer(state: StudioState, action: Action): StudioState {
  switch (action.type) {
    case 'reset': return initialState(action.now);
    case 'clear-message': return { ...state, message: null };
    case 'target': return mapTarget(state, action.id, target => ({ ...target, ...action.patch }));
    case 'axis': return mapTarget(state, action.id, target => ({ ...target, axes: { ...target.axes, [action.key]: Math.min(5, Math.max(1, Math.round(action.value))) }, criteriaChanged: true }));
    case 'candidate': return mapTarget(state, action.targetId, target => ({ ...target, candidates: target.candidates.map(c => c.id === action.id ? { ...c, [action.field]: !c[action.field] } : c) }));
    case 'cart': return { ...state, cart: state.cart.includes(action.id) ? state.cart.filter(id => id !== action.id) : [...state.cart, action.id] };
    case 'start': {
      const target = state.targets.find(t => t.id === action.targetId);
      if (state.instant || !target) return state;
      return { ...state, instant: { ...action, axes: { ...target.axes }, dueAt: state.now + 4500 }, message: null };
    }
    case 'cancel': return { ...state, instant: null, message: 'cancelled' };
    case 'tick': {
      let next = { ...state, now: action.now, missions: state.missions.map(m => {
        if (m.state !== 'ACTIVE') return m;
        if (m.kind === 'reminder' && m.remindAt !== undefined && m.remindAt <= action.now) return finishMission({ ...state, now: action.now }, m, 'FOUND');
        return m.expiresAt <= action.now ? finishMission(state, m, 'EXPIRED') : m;
      }) };
      const instant = state.instant;
      if (!instant || instant.dueAt > action.now) return next;
      next = mapTarget(next, instant.targetId, target => {
        const generation = Math.max(...target.candidates.map(c => c.generation)) + 1;
        const axis = (Object.entries(instant.axes) as [AxisKey, number][]).sort((a, b) => b[1] - a[1])[0][0];
        if (instant.kind === 'expand') {
          const more = target.candidates.find(c => c.hidden);
          // Re-observation restores a hidden product; it never duplicates identity.
          return { ...target, criteriaChanged: JSON.stringify(target.axes) !== JSON.stringify(instant.axes), candidates: target.candidates.map(c => c.id === more?.id ? { ...c, hidden: false, score: null } : c) };
        }
        return { ...target, criteriaChanged: JSON.stringify(target.axes) !== JSON.stringify(instant.axes), candidates: target.candidates.map(c => c.pinned ? c : { ...c, hidden: c.id === 'pelikan', generation, axis, score: c.id === 'custom74' ? 96 : c.score ?? 82 }) };
      });
      return { ...next, instant: null, message: 'research' };
    }
    case 'create': {
      const m = action.mission;
      if (!Number.isFinite(m.expiresAt) || m.expiresAt <= state.now || m.expiresAt > state.now + 30 * DAY) return state;
      if (m.kind === 'reminder') {
        if (m.remindAt !== m.expiresAt || m.price !== undefined || m.minPrice !== undefined) return state;
      } else if (m.price === undefined || !Number.isFinite(m.price) || m.price <= 0 || (m.minPrice !== undefined && (!Number.isFinite(m.minPrice) || m.minPrice < 0 || m.minPrice > m.price))) return state;
      const active = state.missions.filter(mission => mission.state === 'ACTIVE');
      if (active.length >= 10) return { ...state, message: 'limit' };
      if (active.some(x => x.targetId === m.targetId && x.kind === m.kind && x.productId === m.productId && (m.kind === 'reminder' ? x.remindAt === m.remindAt : x.price === m.price && (x.minPrice ?? 0) === (m.minPrice ?? 0) && (Object.keys(m.axes) as AxisKey[]).every(key => x.axes[key] === m.axes[key])))) return { ...state, message: 'duplicate' };
      return { ...state, missions: [...state.missions, m], message: 'added' };
    }
    case 'end': return { ...state, missions: state.missions.map(m => m.id === action.id ? finishMission(state, m, action.outcome) : m), message: 'ended' };
    case 'extend': return { ...state, missions: state.missions.map(m => {
      if (m.id !== action.id || m.state !== 'ACTIVE') return m;
      const expiresAt = Math.min(m.expiresAt + 7 * DAY, state.now + 30 * DAY);
      return { ...m, expiresAt, ...(m.kind === 'reminder' ? { remindAt: expiresAt } : {}) };
    }), message: 'extended' };
  }
}
