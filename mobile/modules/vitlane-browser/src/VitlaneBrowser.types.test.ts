import {
  BrowserAuthorizedCommand,
  isLikelySensitivePublicSearchQuery,
  validateBrowserCommand,
} from './VitlaneBrowser.types';

const NOW = Date.parse('2026-09-24T04:00:00.000Z');

function command(overrides: Partial<BrowserAuthorizedCommand> = {}): BrowserAuthorizedCommand {
  return {
    protocolVersion: 1,
    commandId: 'cmd_1',
    runId: 'run_1',
    deviceId: 'device_1',
    profileRef: 'profile_1',
    sequence: 1,
    leaseEpoch: 1,
    controlGeneration: 2,
    expiresAt: '2026-09-24T04:00:20.000Z',
    actionHash: '1'.repeat(64),
    action: {
      kind: 'scroll',
      direction: 'down',
      reason: 'More results',
      page: {
        tabId: 'ios-main',
        frameId: 'frame_main',
        documentEpoch: 4,
        observationId: 'obs_1',
        topOrigin: 'https://shop.example.com',
        frameOrigin: 'https://shop.example.com',
        pathname: '/products',
        queryOrFragmentPresent: false,
      },
    },
    serverPermit: `hmac-sha256:${'A'.repeat(43)}`,
    ...overrides,
  };
}

describe('validateBrowserCommand', () => {
  it('accepts a fresh main-frame command envelope', () => {
    expect(validateBrowserCommand(command(), NOW)).toEqual({ ok: true });
  });

  it('rejects expired commands before they reach native code', () => {
    const result = validateBrowserCommand(
      command({ expiresAt: '2026-09-24T03:59:59.000Z' }),
      NOW,
    );
    expect(result).toMatchObject({ ok: false, code: 'expired' });
  });

  it('rejects malformed action hashes', () => {
    const result = validateBrowserCommand(
      command({
        actionHash: 'not-a-hash',
      }),
      NOW,
    );
    expect(result).toMatchObject({ ok: false, code: 'invalid_action_hash' });
  });

  it('requires a fresh main-frame observation binding', () => {
    const original = command();
    const result = validateBrowserCommand(
      command({ action: { ...original.action, page: { ...original.action.page, observationId: '' } } }),
      NOW,
    );
    expect(result).toMatchObject({ ok: false, code: 'invalid_page' });
  });

  it('rejects additional action fields even when the command is otherwise typed', () => {
    const original = command();
    const result = validateBrowserCommand(
      command({
        action: { ...original.action, selector: '#unchecked-page-selector' } as never,
      }),
      NOW,
    );
    expect(result).toMatchObject({ ok: false, code: 'policy_denied' });
  });

  it.each([
    'my password is swordfish',
    'otp 123456',
    'card number 4111 1111 1111 1111',
    'person@example.com',
    'https://private.example/path',
    '+82 10 1234 5678',
    '주민등록 900101-1234567',
  ])('rejects sensitive public-search text: %s', query => {
    expect(isLikelySensitivePublicSearchQuery(query)).toBe(true);
    const original = command();
    const result = validateBrowserCommand(
      command({
        action: {
          kind: 'run_preparation_step',
          adapterId: 'builtin.public-search',
          recipeVersion: '1',
          stepId: 'prepare_query',
          bindings: { candidateRef: 'candidate_1', query },
          page: original.action.page,
          reason: 'Stage a query for user review',
        },
      }),
      NOW,
    );
    expect(result).toMatchObject({ ok: false, code: 'policy_denied' });
  });

  it('accepts a non-sensitive shopping query', () => {
    expect(isLikelySensitivePublicSearchQuery('waterproof hiking shoes')).toBe(false);
  });

  it('accepts only an exact, unexpired Coupang approval binding', () => {
    const original = command();
    const approval = {
      approvalId: 'approval_1',
      approvalDigest: `sha256:${'a'.repeat(64)}`,
      currency: 'KRW' as const,
      revision: 1,
      expiresAt: '2026-09-24T04:05:00.000Z',
      totalPriceCeilingKrw: 15_000,
    };
    const item = {
      query: '승인 상품',
      offerIdentity: { productId: '12345', itemId: '23456', vendorItemId: '34567' },
      quantity: 1,
      unitPriceCeilingKrw: 15_000,
      linePriceCeilingKrw: 15_000,
      productUrl: 'https://www.coupang.com/vp/products/12345?itemId=23456&vendorItemId=34567',
      options: { kind: 'none' as const },
    };
    const approvedPreparation = {
      merchantId: 'COUPANG' as const,
      recipeVersion: '1' as const,
      mode: 'single' as const,
      route: 'single_buy_now' as const,
      approval,
      items: [item],
    };
    const bindings = {
      approvedPreparation,
      approval,
      targetLineIndex: 0,
      ...item.offerIdentity,
      quantity: item.quantity,
      unitPriceCeilingKrw: item.unitPriceCeilingKrw,
      linePriceCeilingKrw: item.linePriceCeilingKrw,
      expectedOrigin: 'https://www.coupang.com' as const,
      expectedPath: '/vp/products/12345',
    };
    const approved = command({
      action: {
        kind: 'run_preparation_step',
        adapterId: 'builtin.coupang.purchase-preparation',
        recipeVersion: '1',
        stepId: 'buy_now',
        bindings,
        page: original.action.page,
        reason: 'Approved purchase preparation',
      },
    });
    expect(validateBrowserCommand(approved, NOW)).toEqual({ ok: true });

    const changed = command({
      action: {
        ...approved.action,
        bindings: { ...bindings, quantity: 2 },
      } as never,
    });
    expect(validateBrowserCommand(changed, NOW)).toMatchObject({ ok: false, code: 'policy_denied' });
  });

  it.each([null, ['candidate_1', 'trail shoes']])(
    'rejects non-object preparation bindings without throwing: %p',
    bindings => {
      const original = command();
      const malformed = command({
        action: {
          kind: 'run_preparation_step',
          adapterId: 'builtin.public-search',
          recipeVersion: '1',
          stepId: 'prepare_query',
          bindings,
          page: original.action.page,
          reason: 'Stage a query for user review',
        } as never,
      });
      expect(() => validateBrowserCommand(malformed, NOW)).not.toThrow();
      expect(validateBrowserCommand(malformed, NOW)).toMatchObject({ ok: false, code: 'policy_denied' });
    },
  );
});
