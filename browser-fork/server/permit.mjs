import { createHmac, randomUUID, timingSafeEqual } from 'node:crypto';
import { canonicalize, RequestError, sha256 } from './protocol.mjs';
import {
  buildPurchasePreparationPlan,
  COUPANG_PURCHASE_ADAPTER_ID,
  PURCHASE_PREPARATION_EFFECT,
} from './purchase-recipes.mjs';

function keyBytes(value) {
  const key = Buffer.isBuffer(value) ? value : Buffer.from(value ?? '', 'utf8');
  if (key.length < 32) throw new Error('Command permit key must have at least 32 bytes');
  return key;
}

function unsignedCommand(command) {
  return {
    protocolVersion: command.protocolVersion,
    commandId: command.commandId,
    runId: command.runId,
    deviceId: command.deviceId,
    profileRef: command.profileRef,
    sequence: command.sequence,
    leaseEpoch: command.leaseEpoch,
    controlGeneration: command.controlGeneration,
    expiresAt: command.expiresAt,
    actionHash: command.actionHash,
    action: command.action,
  };
}

function permitFor(command, key) {
  return `hmac-sha256:${createHmac('sha256', key).update(canonicalize(unsignedCommand(command))).digest('base64url')}`;
}

function conflict(message, code) {
  return new RequestError(message, 409, code);
}

function finiteInteger(value, name, min = 0) {
  if (!Number.isSafeInteger(value) || value < min) throw new RequestError(`Invalid ${name}`);
  return value;
}

function boundedId(value, name) {
  if (typeof value !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value)) {
    throw new RequestError(`Invalid ${name}`);
  }
  return value;
}

function validateContext(context) {
  return {
    runId: boundedId(context?.runId, 'runId'),
    deviceId: boundedId(context?.deviceId, 'deviceId'),
    profileRef: boundedId(context?.profileRef, 'profileRef'),
    sequence: finiteInteger(context?.sequence, 'sequence', 1),
    leaseEpoch: finiteInteger(context?.leaseEpoch, 'leaseEpoch', 1),
    controlGeneration: finiteInteger(context?.controlGeneration, 'controlGeneration'),
  };
}

const PURCHASE_PIN_FIELDS = [
  'approvalId',
  'approvalDigest',
  'merchantId',
  'recipeVersion',
  'mode',
];

function isExecutablePurchaseStep(step) {
  return step?.action?.adapterId === COUPANG_PURCHASE_ADAPTER_ID &&
    [PURCHASE_PREPARATION_EFFECT.ACTIVATE, PURCHASE_PREPARATION_EFFECT.VERIFY]
      .includes(step.action.effect);
}

function purchaseProgress(step) {
  const approved = step?.approvedPreparation;
  if (!approved) return undefined;
  const snapshot = approved.action?.bindings?.approvedPreparation;
  const approval = snapshot?.approval;
  if (!snapshot || !approval || approved.merchantId !== snapshot.merchantId ||
      approved.recipeVersion !== snapshot.recipeVersion || approved.mode !== snapshot.mode ||
      approved.action.adapterId !== COUPANG_PURCHASE_ADAPTER_ID ||
      approved.action.recipeVersion !== snapshot.recipeVersion) {
    throw conflict('Approved preparation metadata does not match its snapshot', 'APPROVAL_BINDING_MISMATCH');
  }

  let plan;
  try {
    plan = buildPurchasePreparationPlan({
      merchantId: snapshot.merchantId,
      recipeVersion: snapshot.recipeVersion,
      mode: snapshot.mode,
      approval,
      items: snapshot.items,
    });
  } catch {
    throw conflict('Approved preparation plan is invalid', 'APPROVAL_BINDING_MISMATCH');
  }
  const executableCursors = plan.steps
    .map((planStep, cursor) => isExecutablePurchaseStep(planStep) ? cursor : undefined)
    .filter((cursor) => cursor !== undefined);
  const position = executableCursors.indexOf(approved.cursor);
  if (position < 0 || plan.steps[approved.cursor]?.action.stepId !== approved.action.stepId) {
    throw conflict('Approved preparation cursor is not executable', 'APPROVAL_CURSOR_MISMATCH');
  }
  return {
    approvalId: approval.approvalId,
    approvalDigest: approval.approvalDigest,
    merchantId: snapshot.merchantId,
    recipeVersion: snapshot.recipeVersion,
    mode: snapshot.mode,
    cursor: approved.cursor,
    firstExecutableCursor: executableCursors[0],
    nextExecutableCursor: executableCursors[position + 1] ?? null,
  };
}

function checkPurchaseProgress(state, context, progress) {
  if (!progress) return;
  const pinned = state?.leaseEpoch === context.leaseEpoch ? state.purchase : undefined;
  if (!pinned) {
    if (progress.cursor !== progress.firstExecutableCursor) {
      throw conflict('Approved preparation must begin at its first executable cursor', 'APPROVAL_CURSOR_MISMATCH');
    }
    return;
  }
  if (PURCHASE_PIN_FIELDS.some((field) => pinned[field] !== progress[field])) {
    throw conflict('Approved preparation does not match the lease authority', 'APPROVAL_BINDING_MISMATCH');
  }
  if (pinned.nextExecutableCursor === null || progress.cursor !== pinned.nextExecutableCursor) {
    throw conflict('Approved preparation cursor is not the exact next executable cursor', 'APPROVAL_CURSOR_MISMATCH');
  }
}

function commitPurchaseProgress(state, progress) {
  if (!progress) return;
  if (!state.purchase) {
    state.purchase = Object.fromEntries(PURCHASE_PIN_FIELDS.map((field) => [field, progress[field]]));
  }
  state.purchase.nextExecutableCursor = progress.nextExecutableCursor;
}

export function signAuthorizedCommand(fields, permitKey) {
  const key = keyBytes(permitKey);
  const actionHash = sha256(fields.action);
  const command = {
    protocolVersion: 1,
    commandId: boundedId(fields.commandId, 'commandId'),
    ...validateContext(fields),
    expiresAt: fields.expiresAt,
    actionHash,
    action: fields.action,
  };
  const expiration = Date.parse(command.expiresAt);
  if (typeof command.expiresAt !== 'string' || !Number.isFinite(expiration) ||
      new Date(expiration).toISOString() !== command.expiresAt) throw new RequestError('Invalid expiresAt');
  return { ...command, serverPermit: permitFor(command, key) };
}

export function verifyAuthorizedCommand(command, {
  permitKey,
  now = Date.now,
  expected = {},
} = {}) {
  if (!command || typeof command !== 'object' || Array.isArray(command) || command.protocolVersion !== 1) {
    throw new RequestError('Invalid authorized command', 400, 'INVALID_COMMAND');
  }
  const normalized = {
    protocolVersion: 1,
    commandId: boundedId(command.commandId, 'commandId'),
    ...validateContext(command),
    expiresAt: command.expiresAt,
    actionHash: command.actionHash,
    action: command.action,
  };
  if (!command.action || typeof command.action !== 'object' || Array.isArray(command.action) ||
      typeof command.actionHash !== 'string' || !/^[a-f0-9]{64}$/.test(command.actionHash) ||
      sha256(command.action) !== command.actionHash) {
    throw new RequestError('Action hash mismatch', 409, 'ACTION_HASH_MISMATCH');
  }
  const expiration = Date.parse(command.expiresAt);
  const currentTime = now();
  if (typeof command.expiresAt !== 'string' || !Number.isFinite(expiration) ||
      new Date(expiration).toISOString() !== command.expiresAt) {
    throw new RequestError('Invalid command expiry', 400, 'INVALID_COMMAND');
  }
  if (expiration <= currentTime) throw conflict('Authorized command has expired', 'COMMAND_EXPIRED');
  if (expiration - currentTime > 60_000) throw conflict('Authorized command expiry is too far in the future', 'INVALID_COMMAND_EXPIRY');
  for (const field of ['runId', 'deviceId', 'profileRef', 'sequence', 'leaseEpoch', 'controlGeneration']) {
    if (expected[field] !== undefined && normalized[field] !== expected[field]) {
      throw conflict(`Command ${field} does not match local state`, 'COMMAND_BINDING_MISMATCH');
    }
  }
  const permit = command.serverPermit;
  if (typeof permit !== 'string' || !/^hmac-sha256:[A-Za-z0-9_-]{43}$/.test(permit)) {
    throw new RequestError('Missing command permit', 401, 'INVALID_SERVER_PERMIT');
  }
  const given = Buffer.from(permit.slice('hmac-sha256:'.length), 'base64url');
  const wanted = Buffer.from(permitFor(normalized, keyBytes(permitKey)).slice('hmac-sha256:'.length), 'base64url');
  if (given.length !== wanted.length || !timingSafeEqual(given, wanted)) {
    throw new RequestError('Invalid command permit', 401, 'INVALID_SERVER_PERMIT');
  }
  return { ...normalized, serverPermit: permit };
}

/**
 * Issues monotonic, short-lived commands. A repeated identical request for an
 * already-issued sequence gets the exact same command; a changed payload for
 * that sequence is a replay conflict. The model call is also coalesced while
 * an identical request is in flight.
 */
export function createCommandIssuer({
  permitKey,
  now = Date.now,
  commandId = randomUUID,
  ttlMs = 15_000,
  maxRuns = 1_000,
} = {}) {
  const key = keyBytes(permitKey);
  if (!Number.isSafeInteger(ttlMs) || ttlMs < 1_000 || ttlMs > 60_000) throw new Error('ttlMs must be between 1s and 60s');
  if (!Number.isSafeInteger(maxRuns) || maxRuns < 1) throw new Error('maxRuns must be positive');
  const runs = new Map();
  const pending = new Map();

  function requestKey(context) {
    return `${context.runId}\u0000${context.leaseEpoch}\u0000${context.sequence}`;
  }

  function issuedFor(state, context) {
    if (!state || state.leaseEpoch !== context.leaseEpoch) return undefined;
    return state.commands.get(context.sequence);
  }

  function checkBinding(state, context) {
    if (!state) {
      if (context.sequence !== 1) throw conflict('A new run must begin at sequence 1', 'OUT_OF_SEQUENCE');
      if (runs.size >= maxRuns) throw new RequestError('Command issuer capacity reached', 503, 'ISSUER_CAPACITY');
      return;
    }
    if (state.deviceId !== context.deviceId || state.profileRef !== context.profileRef) {
      throw conflict('Run is bound to another device or profile', 'COMMAND_BINDING_MISMATCH');
    }
    if (context.leaseEpoch < state.leaseEpoch) throw conflict('Stale device lease', 'STALE_LEASE');
    if (context.leaseEpoch > state.leaseEpoch) {
      if (context.sequence !== 1) throw conflict('A new lease must begin at sequence 1', 'OUT_OF_SEQUENCE');
      if (context.controlGeneration < state.controlGeneration) throw conflict('Stale control generation', 'STALE_GENERATION');
      return;
    }
    if (context.controlGeneration < state.controlGeneration) throw conflict('Stale control generation', 'STALE_GENERATION');
    if (context.sequence !== state.lastSequence + 1) throw conflict('Command sequence is not next', 'OUT_OF_SEQUENCE');
  }

  async function authorize(step, propose) {
    const context = validateContext(step?.commandContext);
    if (typeof step?.requestHash !== 'string' || !/^[a-f0-9]{64}$/.test(step.requestHash)) {
      throw new RequestError('Invalid request hash');
    }
    const state = runs.get(context.runId);
    const issued = issuedFor(state, context);
    if (issued) {
      if (context.controlGeneration < state.controlGeneration) throw conflict('Stale control generation', 'STALE_GENERATION');
      if (context.sequence !== state.lastSequence) throw conflict('Command sequence is no longer current', 'OUT_OF_SEQUENCE');
      if (issued.requestHash !== step.requestHash) throw conflict('Sequence was already used for a different request', 'REPLAY_CONFLICT');
      if (Date.parse(issued.command.expiresAt) <= now()) throw conflict('Replayed command has expired; reobserve', 'COMMAND_EXPIRED');
      return issued.command;
    }

    const keyForRequest = requestKey(context);
    const inFlight = pending.get(keyForRequest);
    if (inFlight) {
      if (inFlight.requestHash !== step.requestHash) throw conflict('Sequence is in use by a different request', 'REPLAY_CONFLICT');
      return inFlight.promise;
    }
    checkBinding(state, context);
    const progress = purchaseProgress(step);
    checkPurchaseProgress(state, context, progress);

    const promise = (async () => {
      const action = await propose();
      const issuedAt = now();
      const command = signAuthorizedCommand({
        commandId: commandId(),
        ...context,
        expiresAt: new Date(issuedAt + ttlMs).toISOString(),
        action,
      }, key);
      const current = runs.get(context.runId);
      checkBinding(current, context);
      checkPurchaseProgress(current, context, progress);
      const next = context.leaseEpoch > (current?.leaseEpoch ?? 0) ? {
        deviceId: context.deviceId,
        profileRef: context.profileRef,
        leaseEpoch: context.leaseEpoch,
        controlGeneration: context.controlGeneration,
        lastSequence: 0,
        commands: new Map(),
        purchase: undefined,
      } : (current ?? {
        deviceId: context.deviceId,
        profileRef: context.profileRef,
        leaseEpoch: context.leaseEpoch,
        controlGeneration: context.controlGeneration,
        lastSequence: 0,
        commands: new Map(),
        purchase: undefined,
      });
      next.controlGeneration = context.controlGeneration;
      next.lastSequence = context.sequence;
      next.commands.set(context.sequence, { requestHash: step.requestHash, command });
      commitPurchaseProgress(next, progress);
      while (next.commands.size > 20) next.commands.delete(next.commands.keys().next().value);
      runs.set(context.runId, next);
      return command;
    })();
    pending.set(keyForRequest, { requestHash: step.requestHash, promise });
    try { return await promise; }
    finally { pending.delete(keyForRequest); }
  }

  return { authorize, verify: (command, options = {}) => verifyAuthorizedCommand(command, { ...options, permitKey: key }) };
}
