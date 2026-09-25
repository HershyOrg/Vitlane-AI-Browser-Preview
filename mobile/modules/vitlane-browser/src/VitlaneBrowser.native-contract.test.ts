/** @jest-environment node */
/// <reference types="node" />

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import type { BrowserCapabilities } from './VitlaneBrowser.types';

const VIEW_SOURCE = readFileSync(resolve(__dirname, '../ios/VitlaneBrowserView.swift'), 'utf8');
const CONTRACT_SOURCE = readFileSync(resolve(__dirname, '../ios/VitlaneBrowserContracts.swift'), 'utf8');

describe('Vitlane iOS browser native contract', () => {
  it('uses the persistent app-owned WebKit store for same-session login and checkout', () => {
    expect(VIEW_SOURCE).toContain('configuration.websiteDataStore = .default()');
    expect(VIEW_SOURCE).not.toContain('configuration.websiteDataStore = .nonPersistent()');
  });

  it('keeps native and TypeScript session capabilities aligned', () => {
    const expected: Pick<BrowserCapabilities, 'sessionPersistence' | 'sameSessionCheckout'> = {
      sessionPersistence: 'app-scoped',
      sameSessionCheckout: true,
    };

    expect(expected).toEqual({
      sessionPersistence: 'app-scoped',
      sameSessionCheckout: true,
    });
    expect(VIEW_SOURCE).toContain('"sessionPersistence": "app-scoped"');
    expect(VIEW_SOURCE).toContain('"sameSessionCheckout": true');
  });

  it('keeps account, session and cart routes under user control', () => {
    expect(CONTRACT_SOURCE).toContain('"account", "profile", "order"');
    expect(CONTRACT_SOURCE).toContain('"session", "cancel/confirm"');
    expect(CONTRACT_SOURCE).toContain('Set(["cart", "basket", "bag"])');
  });

  it('reports sensitive navigation while the user already controls the browser', () => {
    expect(VIEW_SOURCE).toContain('if mode == .paused && runId == nil');
    expect(VIEW_SOURCE).toMatch(
      /if !VitlaneURLPolicy\.isPublicAgentURL\(webView\.url\)[\s\S]*else if let reason = VitlaneURLPolicy\.nativeSensitiveReason\(webView\.url\)/,
    );
    expect(VIEW_SOURCE).not.toMatch(
      /if mode == \.agent \{\s*if !VitlaneURLPolicy\.isPublicAgentURL\(webView\.url\)/,
    );
  });

  it('projects the page-authored session hint as untrusted data', () => {
    expect(VIEW_SOURCE).toContain('"sessionStateHint": sanitizeSessionStateHint');
    expect(VIEW_SOURCE).toContain('"sessionStateHint": "unknown"');
  });

  it('does not let native chrome resume AI without the app observation gate', () => {
    expect(VIEW_SOURCE).toContain('guard mode == .agent else {');
    expect(VIEW_SOURCE).toContain('takeOverButton.isHidden = !canTakeOver');
    expect(VIEW_SOURCE).not.toMatch(
      /private func takeOverOrResumeFromNativeBar\(\)[\s\S]*?transition\(to: \.agent, reason: "agent_resumed"/,
    );
  });
});
