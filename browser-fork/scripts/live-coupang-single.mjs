#!/usr/bin/env node

import { randomBytes } from 'node:crypto';
import { constants as fsConstants } from 'node:fs';
import { access, mkdtemp, readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { homedir, tmpdir } from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { chromium } from 'playwright';
import {
  buildSingleApproval,
  executeApprovedSingle,
  parseCoupangProductUrl,
  parseOptionLines,
} from './lib/live-coupang-single.mjs';

const LOOPBACK_HOST = '127.0.0.1';
const MAX_BODY_BYTES = 16_384;
const DEFAULT_CHROME_PATH = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const chromePath = process.env.VITLANE_CHROME_PATH || DEFAULT_CHROME_PATH;
const controlToken = randomBytes(32).toString('base64url');
const controlPath = `/control/${randomBytes(18).toString('base64url')}`;
const cspNonce = randomBytes(18).toString('base64url');
const pageAgentSource = await readFile(new URL('../agent/page-agent.js', import.meta.url), 'utf8');
const permitKey = randomBytes(32);

const runtime = {
  phase: 'starting',
  message: 'Vitlane 전용 Chrome을 시작하고 있습니다.',
  product: null,
  preview: null,
  executing: false,
  complete: false,
  terminal: false,
};

let chromeProcess;
let profileDirectory;
let controlOrigin;
let expectedHost;
let browserOperationActive = false;

function json(res, status, body) {
  const payload = Buffer.from(JSON.stringify(body));
  res.writeHead(status, {
    'Cache-Control': 'no-store',
    'Content-Length': payload.length,
    'Content-Type': 'application/json; charset=utf-8',
    'Cross-Origin-Resource-Policy': 'same-origin',
    'X-Content-Type-Options': 'nosniff',
  });
  res.end(payload);
}

function publicState() {
  return {
    phase: runtime.phase,
    message: runtime.message,
    product: runtime.product,
    preview: runtime.preview?.summary ?? null,
    executing: runtime.executing,
    complete: runtime.complete,
    terminal: runtime.terminal,
  };
}

function fail(message, code = 'LIVE_TRIAL_FAILED', status = 400) {
  const error = new Error(message);
  error.code = code;
  error.status = status;
  return error;
}

function requireControlRequest(req) {
  if (req.headers.host !== expectedHost) throw fail('잘못된 로컬 Host 요청입니다.', 'HOST_DENIED', 403);
  if (req.headers.origin !== controlOrigin) throw fail('다른 출처의 제어 요청은 거절됩니다.', 'ORIGIN_DENIED', 403);
  if (req.headers['x-vitlane-control'] !== controlToken) {
    throw fail('로컬 제어 토큰이 일치하지 않습니다.', 'CONTROL_TOKEN_DENIED', 403);
  }
  const fetchSite = req.headers['sec-fetch-site'];
  if (fetchSite && fetchSite !== 'same-origin') {
    throw fail('교차 사이트 제어 요청은 거절됩니다.', 'CROSS_SITE_DENIED', 403);
  }
}

async function readJson(req) {
  if (req.headers['content-type']?.split(';', 1)[0].trim() !== 'application/json') {
    throw fail('JSON 요청만 허용됩니다.', 'INVALID_CONTENT_TYPE', 415);
  }
  let size = 0;
  const chunks = [];
  for await (const chunk of req) {
    size += chunk.length;
    if (size > MAX_BODY_BYTES) throw fail('요청이 너무 큽니다.', 'REQUEST_TOO_LARGE', 413);
    chunks.push(chunk);
  }
  try {
    return JSON.parse(Buffer.concat(chunks).toString('utf8'));
  } catch {
    throw fail('JSON 요청을 읽을 수 없습니다.', 'INVALID_JSON');
  }
}

async function waitForDevToolsPort() {
  const activePortFile = path.join(profileDirectory, 'DevToolsActivePort');
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (chromeProcess.exitCode !== null) throw fail('Chrome이 시작 중 종료되었습니다.', 'CHROME_START_FAILED', 500);
    try {
      const [rawPort] = (await readFile(activePortFile, 'utf8')).trim().split(/\r?\n/);
      const port = Number(rawPort);
      if (Number.isSafeInteger(port) && port >= 1 && port <= 65_535) return port;
    } catch {
      // Chrome creates DevToolsActivePort only after its dedicated profile is ready.
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw fail('Chrome 제어 포트를 찾지 못했습니다.', 'CHROME_START_TIMEOUT', 500);
}

async function connectedBrowser() {
  const port = await waitForDevToolsPort();
  return chromium.connectOverCDP(`http://${LOOPBACK_HOST}:${port}`, { timeout: 10_000 });
}

async function withExactProductPage(usePage) {
  if (runtime.terminal) {
    throw fail('이 실행은 종료되었습니다. Chrome을 닫고 새 실행기를 시작해 주세요.', 'LIVE_TRIAL_TERMINAL', 409);
  }
  if (browserOperationActive) {
    throw fail('다른 로컬 브라우저 작업이 진행 중입니다.', 'BROWSER_OPERATION_BUSY', 409);
  }
  browserOperationActive = true;
  let connected;
  try {
    connected = await connectedBrowser();
    const matches = [];
    for (const context of connected.contexts()) {
      for (const page of context.pages()) {
        try {
          const parsed = parseCoupangProductUrl(page.url());
          matches.push({
            page,
            identity: {
              ...parsed.offerIdentity,
              canonicalUrl: parsed.productUrl,
            },
          });
        } catch {
          // URL identity is used only to find the exact public product tab.
          // DOM from control, login, account, checkout, and other tabs is never
          // read. Only one matching product URL may enter the route.
        }
      }
    }
    if (matches.length === 0) {
      throw fail('itemId와 vendorItemId가 포함된 쿠팡 상품 탭을 먼저 열어 주세요.', 'PRODUCT_TAB_NOT_FOUND');
    }
    if (matches.length !== 1) {
      throw fail('쿠팡 상품 탭을 하나만 남긴 뒤 다시 확인해 주세요.', 'PRODUCT_TAB_AMBIGUOUS');
    }
    return await usePage(matches[0]);
  } finally {
    // `Browser.close()` on a browser obtained via connectOverCDP disconnects
    // this controller while leaving the independently launched Chrome open.
    // This also drops Playwright's browser-level target attachment after each
    // bounded operation; the user keeps the visible merchant session.
    try { await connected?.close(); } catch { /* Chrome may have been closed by the user. */ }
    browserOperationActive = false;
  }
}

function exactObject(value, keys) {
  return value && typeof value === 'object' && !Array.isArray(value) &&
    Object.keys(value).length === keys.length && keys.every((key) => key in value);
}

function positiveInteger(value, name, maximum) {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 1 || value > maximum) {
    throw fail(`${name} 값이 올바르지 않습니다.`, 'INVALID_APPROVAL_INPUT');
  }
  return value;
}

async function createPreview(body) {
  if (!exactObject(body, ['quantity', 'unitPriceCeilingKrw', 'totalPriceCeilingKrw', 'optionsText'])) {
    throw fail('승인 입력 형식이 올바르지 않습니다.', 'INVALID_APPROVAL_INPUT');
  }
  if (typeof body.optionsText !== 'string' || body.optionsText.length > 1_000) {
    throw fail('옵션 입력은 1,000자 이하여야 합니다.', 'INVALID_APPROVAL_INPUT');
  }
  const quantity = positiveInteger(body.quantity, '수량', 99);
  const unitPriceCeilingKrw = positiveInteger(body.unitPriceCeilingKrw, '개당 최대 가격', 1_000_000_000_000);
  const totalPriceCeilingKrw = positiveInteger(body.totalPriceCeilingKrw, '전체 최대 가격', 1_000_000_000_000);
  if (unitPriceCeilingKrw * quantity > totalPriceCeilingKrw) {
    throw fail('전체 최대 가격은 개당 최대 가격 × 수량 이상이어야 합니다.', 'INVALID_APPROVAL_INPUT');
  }

  return withExactProductPage(async ({ page, identity }) => {
    const options = parseOptionLines(body.optionsText);
    const approvedInput = buildSingleApproval({
      productUrl: identity.canonicalUrl,
      quantity,
      unitPriceCeilingKrw,
      totalPriceCeilingKrw,
      options,
    });
    const previewToken = randomBytes(24).toString('base64url');
    const summary = {
      previewToken,
      merchant: '쿠팡',
      productId: identity.productId,
      itemId: identity.itemId,
      vendorItemId: identity.vendorItemId,
      productUrl: identity.canonicalUrl,
      quantity,
      unitPriceCeilingKrw,
      totalPriceCeilingKrw,
      options: options.kind === 'choices' ? options.choices : [],
      expiresAt: approvedInput.approval.expiresAt,
    };
    runtime.product = {
      productId: identity.productId,
      itemId: identity.itemId,
      vendorItemId: identity.vendorItemId,
      productUrl: identity.canonicalUrl,
    };
    runtime.preview = { previewToken, approvedInput, productUrl: page.url(), summary, consumed: false };
    runtime.phase = 'approval_ready';
    runtime.message = '아래 승인 범위를 확인해 주세요.';
    return summary;
  });
}

async function executePreview(body) {
  if (!exactObject(body, ['previewToken', 'acknowledged']) || body.acknowledged !== true ||
      typeof body.previewToken !== 'string') {
    throw fail('명시적인 승인 확인이 필요합니다.', 'APPROVAL_REQUIRED');
  }
  const preview = runtime.preview;
  if (!preview || preview.consumed || preview.previewToken !== body.previewToken) {
    throw fail('승인 미리보기가 없거나 이미 사용되었습니다. 다시 확인해 주세요.', 'APPROVAL_STALE', 409);
  }
  if (runtime.executing || runtime.complete) throw fail('이미 실행 중이거나 완료된 승인입니다.', 'APPROVAL_STALE', 409);

  return withExactProductPage(async ({ page, identity }) => {
    if (page.url() !== preview.productUrl || identity.canonicalUrl !== preview.summary.productUrl) {
      throw fail('승인 후 상품 탭이 변경되었습니다. 다시 확인해 주세요.', 'PRODUCT_CHANGED', 409);
    }

    preview.consumed = true;
    runtime.executing = true;
    runtime.phase = 'running';
    runtime.message = '승인 범위를 다시 검증하고 있습니다.';
    try {
      await page.bringToFront();
      const result = await executeApprovedSingle({
        page,
        approvedInput: preview.approvedInput,
        permitKey,
        pageAgentSource,
        onStep(event) {
          runtime.message = event.message ?? `단계 실행: ${event.stepId}`;
        },
      });
      runtime.complete = true;
      runtime.terminal = true;
      runtime.phase = 'handoff';
      runtime.message = '바로구매를 한 번 활성화했습니다. 주문서 확인과 최종 결제는 직접 진행해 주세요.';
      return result;
    } catch (error) {
      if (error?.buyNowAttempted === true) {
        runtime.terminal = true;
        runtime.phase = 'handoff_unknown';
        runtime.message = '바로구매가 실행되었을 수 있어 이 실행을 종료했습니다. 보이는 쿠팡 화면을 직접 확인하고 다시 실행하지 마세요.';
      } else {
        runtime.phase = 'blocked';
        runtime.message = `자동 조작을 중단했습니다: ${error.message}`;
      }
      throw error;
    } finally {
      runtime.executing = false;
    }
  });
}

function controlPage() {
  const token = JSON.stringify(controlToken);
  return `<!doctype html>
<html lang="ko"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Vitlane · 쿠팡 live 구매 준비</title>
<style nonce="${cspNonce}">
:root{color-scheme:light;--ink:#17200f;--muted:#63705d;--line:#dce2d6;--paper:#fbfcf8;--card:#fff;--accent:#baff68;--warn:#fff2c8}
*{box-sizing:border-box}body{margin:0;background:var(--paper);color:var(--ink);font-family:-apple-system,BlinkMacSystemFont,"Pretendard",sans-serif}
main{width:min(720px,calc(100% - 32px));margin:32px auto 80px}.eyebrow{font-size:12px;font-weight:700;letter-spacing:.08em;text-transform:uppercase;color:var(--muted)}
h1{font-size:30px;line-height:1.2;margin:8px 0 12px}p{line-height:1.6;color:var(--muted)}.card{background:var(--card);border:1px solid var(--line);border-radius:20px;padding:20px;margin:16px 0}
.notice{background:var(--warn);border-color:#e4c867}.row{display:grid;grid-template-columns:1fr 1fr;gap:12px}.field{display:grid;gap:7px;margin:12px 0}label{font-size:13px;font-weight:700}
input,textarea{width:100%;border:1px solid #cbd3c4;border-radius:12px;background:#fff;padding:12px;font:inherit;color:var(--ink)}textarea{min-height:92px;resize:vertical}
button{appearance:none;border:0;border-radius:999px;background:var(--accent);color:var(--ink);font:700 15px inherit;padding:13px 18px;cursor:pointer}button.secondary{background:#edf1e8}button:disabled{opacity:.45;cursor:not-allowed}
.status{font-weight:700;color:var(--ink)}dl{display:grid;grid-template-columns:150px 1fr;gap:8px 12px;margin:0}dt{color:var(--muted)}dd{margin:0;overflow-wrap:anywhere}.hidden{display:none}.check{display:flex;align-items:flex-start;gap:10px;margin:16px 0}.check input{width:20px;height:20px;margin:0}.check label{line-height:1.45}.error{color:#9b2c18;font-weight:700}
@media(max-width:560px){.row{grid-template-columns:1fr}dl{grid-template-columns:1fr}dd{margin-bottom:8px}}
</style></head><body><main>
<div class="eyebrow">Vitlane live browser trial</div><h1>실제 쿠팡 상품 구매 준비</h1>
<p>Vitlane 전용 Chrome 프로필에서 로그인하고 상품 페이지를 하나만 연 뒤 사용하세요. 개인정보·쿠키는 이 로컬 프로세스 밖으로 보내지 않습니다.</p>
<section class="card notice"><strong>자동화 경계</strong><p>승인한 상품·옵션·수량·가격을 확인한 뒤 <b>바로구매</b>만 한 번 누릅니다. 주문서 확인, 배송지, 결제수단, 최종 주문은 직접 진행합니다.</p></section>
<section class="card"><h2>1. 상품 탭 확인</h2><p>쿠팡 탭에서 로그인하고, 주소에 <code>itemId</code>와 <code>vendorItemId</code>가 포함된 상품을 여세요.</p><button id="detect" class="secondary">현재 상품 확인</button><p id="status" class="status">Chrome 시작 중…</p></section>
<section id="scope" class="card hidden"><h2>2. 승인 범위 입력</h2><dl id="product"></dl>
<div class="row"><div class="field"><label for="quantity">수량</label><input id="quantity" type="number" min="1" max="99" value="1"></div><div class="field"><label for="unit">개당 최대 가격 (원)</label><input id="unit" type="number" min="1" value="50000"></div></div>
<div class="field"><label for="total">전체 최대 가격 (원)</label><input id="total" type="number" min="1" value="50000"></div>
<div class="field"><label for="options">선택 옵션 · 줄마다 정확한 그룹=값 (없으면 비움)</label><textarea id="options" placeholder="예: 색상=블랙&#10;사이즈=270"></textarea></div>
<button id="preview">승인 내용 미리보기</button></section>
<section id="approval" class="card hidden"><h2>3. 최종 승인</h2><dl id="summary"></dl><div class="check"><input id="ack" type="checkbox"><label for="ack">위 범위로 실제 쿠팡 페이지의 바로구매 버튼을 한 번 누르는 데 동의합니다.</label></div><button id="execute" disabled>승인하고 바로구매 준비</button></section>
<p id="error" class="error"></p>
</main><script nonce="${cspNonce}">
const token=${token};const byId=(id)=>document.getElementById(id);let previewToken='';
async function api(path,body={}){const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json','X-Vitlane-Control':token},body:JSON.stringify(body)});const data=await response.json();if(!response.ok)throw new Error(data.error||data.code||'요청 실패');return data}
function money(value){return new Intl.NumberFormat('ko-KR').format(value)+'원'}
function renderProduct(product){byId('product').replaceChildren(...Object.entries({'productId':product.productId,'itemId':product.itemId,'vendorItemId':product.vendorItemId}).flatMap(([k,v])=>{const dt=document.createElement('dt');dt.textContent=k;const dd=document.createElement('dd');dd.textContent=v;return[dt,dd]}));byId('scope').classList.remove('hidden')}
function renderSummary(value){const entries=[['상품',value.productId+' / '+value.itemId+' / '+value.vendorItemId],['수량',String(value.quantity)],['개당 최대',money(value.unitPriceCeilingKrw)],['전체 최대',money(value.totalPriceCeilingKrw)],['옵션',value.options.length?value.options.map(x=>x.groupName+'='+x.valueName).join(', '):'없음'],['승인 만료',new Date(value.expiresAt).toLocaleTimeString('ko-KR')]];byId('summary').replaceChildren(...entries.flatMap(([k,v])=>{const dt=document.createElement('dt');dt.textContent=k;const dd=document.createElement('dd');dd.textContent=v;return[dt,dd]}));previewToken=value.previewToken;byId('approval').classList.remove('hidden');byId('ack').checked=false;byId('execute').disabled=true}
async function run(fn){byId('error').textContent='';try{await fn()}catch(error){byId('error').textContent=error.message}}
byId('detect').onclick=()=>run(async()=>{const data=await api('/api/product');renderProduct(data.product);byId('status').textContent=data.message});
byId('preview').onclick=()=>run(async()=>{const data=await api('/api/preview',{quantity:Number(byId('quantity').value),unitPriceCeilingKrw:Number(byId('unit').value),totalPriceCeilingKrw:Number(byId('total').value),optionsText:byId('options').value});renderSummary(data.preview)});
byId('ack').onchange=()=>{byId('execute').disabled=!byId('ack').checked};
byId('execute').onclick=()=>run(async()=>{byId('execute').disabled=true;byId('status').textContent='승인 범위를 다시 검증하고 실행합니다…';await api('/api/execute',{previewToken,acknowledged:true});byId('status').textContent='바로구매를 활성화했습니다. 쿠팡 주문서를 직접 확인하세요.'});
setInterval(()=>fetch('/api/status',{headers:{'X-Vitlane-Control':token}}).then(r=>r.json()).then(s=>{byId('status').textContent=s.message;if(s.terminal){byId('detect').disabled=true;byId('preview').disabled=true;byId('execute').disabled=true}}).catch(()=>{}),1000);
</script></body></html>`;
}

const server = createServer(async (req, res) => {
  try {
    const requestUrl = new URL(req.url, controlOrigin);
    if (req.headers.host !== expectedHost) throw fail('잘못된 로컬 Host 요청입니다.', 'HOST_DENIED', 403);
    if (req.method === 'GET' && requestUrl.pathname === controlPath) {
      const body = Buffer.from(controlPage());
      res.writeHead(200, {
        'Cache-Control': 'no-store',
        'Content-Length': body.length,
        'Content-Security-Policy': `default-src 'none'; script-src 'nonce-${cspNonce}'; style-src 'nonce-${cspNonce}'; connect-src 'self'; img-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`,
        'Content-Type': 'text/html; charset=utf-8',
        'Cross-Origin-Opener-Policy': 'same-origin',
        'Cross-Origin-Resource-Policy': 'same-origin',
        'Referrer-Policy': 'no-referrer',
        'X-Content-Type-Options': 'nosniff',
        'X-Frame-Options': 'DENY',
      });
      res.end(body);
      return;
    }
    if (requestUrl.pathname === '/favicon.ico') { res.writeHead(204); res.end(); return; }
    if (req.method === 'GET' && requestUrl.pathname === '/api/status') {
      if (req.headers['x-vitlane-control'] !== controlToken) throw fail('제어 토큰이 필요합니다.', 'CONTROL_TOKEN_DENIED', 403);
      json(res, 200, publicState());
      return;
    }
    if (req.method !== 'POST') throw fail('지원하지 않는 경로입니다.', 'NOT_FOUND', 404);
    requireControlRequest(req);
    const body = await readJson(req);
    if (requestUrl.pathname === '/api/product') {
      const response = await withExactProductPage(async ({ identity }) => {
        runtime.product = {
          productId: identity.productId,
          itemId: identity.itemId,
          vendorItemId: identity.vendorItemId,
          productUrl: identity.canonicalUrl,
        };
        runtime.preview = null;
        runtime.phase = 'product_ready';
        runtime.message = '상품을 확인했습니다. 승인 범위를 입력해 주세요.';
        return { product: runtime.product, message: runtime.message };
      });
      json(res, 200, response);
      return;
    }
    if (requestUrl.pathname === '/api/preview') {
      json(res, 200, { preview: await createPreview(body) });
      return;
    }
    if (requestUrl.pathname === '/api/execute') {
      json(res, 200, { result: await executePreview(body), state: publicState() });
      return;
    }
    throw fail('지원하지 않는 경로입니다.', 'NOT_FOUND', 404);
  } catch (error) {
    json(res, error.status ?? 500, {
      error: error.status ? error.message : '로컬 실행기 오류로 자동 조작을 중단했습니다.',
      code: error.code ?? 'LIVE_TRIAL_FAILED',
    });
  }
});

await access(chromePath, fsConstants.X_OK).catch(() => {
  throw new Error(`Google Chrome 실행 파일을 찾을 수 없습니다: ${chromePath}`);
});

await new Promise((resolve, reject) => {
  server.once('error', reject);
  server.listen(0, LOOPBACK_HOST, resolve);
});
const address = server.address();
expectedHost = `${LOOPBACK_HOST}:${address.port}`;
controlOrigin = `http://${expectedHost}`;
const controlUrl = `${controlOrigin}${controlPath}`;
profileDirectory = await mkdtemp(path.join(tmpdir(), 'vitlane-live-coupang-'));
chromeProcess = spawn(chromePath, [
  `--user-data-dir=${profileDirectory}`,
  '--remote-debugging-address=127.0.0.1',
  '--remote-debugging-port=0',
  '--no-first-run',
  '--no-default-browser-check',
  '--new-window',
  controlUrl,
  'https://www.coupang.com/',
], { detached: true, stdio: 'ignore' });
chromeProcess.unref();
runtime.phase = 'ready';
runtime.message = '쿠팡 탭에서 로그인하고 상품 페이지를 하나만 연 뒤 현재 상품 확인을 누르세요.';

console.log(`Vitlane live Coupang control: ${controlUrl}`);
console.log(`Dedicated Chrome profile: ${profileDirectory}`);
console.log('Chrome을 닫으면 전용 임시 프로필을 직접 삭제할 수 있습니다. 개인 Chrome 프로필은 사용하지 않습니다.');

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.once(signal, () => {
    server.close(() => process.exit(0));
  });
}
