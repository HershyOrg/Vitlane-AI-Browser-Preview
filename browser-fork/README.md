# Vitlane Browser Fork

Vitlane 모바일에서 판매처 페이지를 읽고 제한된 준비 동작을 수행하기 위한 Android Chromium patch,
iOS WebKit adapter와 개발용 AI bridge입니다. 이 디렉터리는 이전 standalone proof를 Vitlane monorepo로
가져와 protocol v1과 사용자 제어 경계에 맞게 보강한 source입니다.

## 현재 상태

| 항목 | 상태 |
|---|---|
| 공통 JSON Schema·golden fixture | 구현, 자동 테스트 통과 |
| OpenAI-compatible bridge와 signed command | 구현, local test provider로 통과 |
| 서버 측 판매처 검색 `POST /v1/discover` | 8개 검토 entry, 11번가·무신사·컬리 Go CandidatePool 연결 |
| isolated-world page agent | desktop Chrome fixture 통과 |
| macOS 공식 Chrome live harness | 단일 쿠팡 상품의 별도 profile·로컬 승인·서명 단계 구현; 실계정 smoke 대기 |
| pinned Chromium patch 재현성 | hash·apply 검증 통과 |
| Android Chromium native source | 현재 쿠팡 상품 1개의 승인 패널 → 옵션·수량 검증 → `바로구매` 실행 경로 구현 |
| Android Chromium native compile/APK | **미실행** |
| Android 실기기 | **미실행** |
| iOS Expo local module | 쿠팡 고정 recipe validator/executor, 승인 digest 재계산·step 순서 고정과 1회 navigation handoff source 구현; Swift policy smoke 통과, Xcode build 미실행 |
| 실제 AI provider·live 판매처 smoke | Managed planning과 익명 discovery 실행; native browser 행동은 미실행 |
| Go durable BrowserRun journal | REST/PostgreSQL source·test 구현 |
| Go Device Channel·기기 permit enrollment | **미구현** |

따라서 현재 결과를 “설치 가능한 AI 브라우저가 완성됐다”거나 “실제 구매가 된다”고 표현하지 않는다.
Desktop fixture는 protocol/page-agent 동작을, RN screenshot은 공통 UX만 증명한다.

APK 전에도 실제 판매처 세션에서 단일 상품 경로를 검수할 수 있도록 macOS 공식 Chrome 전용
profile을 여는 `npm run try:coupang`을 제공한다. fixture 화면이 아니라 사용자가 직접 로그인한 쿠팡
상품 탭을 대상으로 하지만, 실사이트 DOM 검증 전에는 성공을 주장하지 않는다. 정확한 승인 절차와
중단 조건은 [`macOS 쿠팡 실브라우저 검수`](docs/live-coupang-mac.md)에 있다.

`POST /v1/discover`는 bridge가 실행되는 로컬 서버에서 Playwright로 공개 판매처 검색 결과 후보를 읽는
별도 기능이다. Android Chromium 또는 iOS `WKWebView`를 조작하지 않으며, native navigation policy,
signed command 실행, device journal이나 실제 기기 browser 동작을 검증하지 않는다.

## 구조

```text
mobile RN Run / Browser / Handoff / Result UI
      │ typed state, authority 없음
      ├─ Android Chromium native host + pinned patch
      └─ iOS WKWebView adapter (mobile/modules/vitlane-browser)
             │ fixed isolated-world page agent
             ▼
      SanitizedObservation
             ▼
      Vitlane development bridge → model provider
             ▼
      HMAC-signed AuthorizedCommand
             ▼
      native binding/policy check → one allowlisted action → fresh observation
```

모델에는 raw CDP, 임의 JavaScript, cookie/password, shell, 파일, clipboard 또는 결제·예약·취소
확정 도구를 주지 않는다. 일반 v1 page agent는 viewport scroll과 빈 GET searchbox에 공개 검색어를
준비할 수 있다. `open_candidate`는 redirect와 DNS 결과까지 통제하는 native navigation policy가
없으므로 자동 이동하지 않고 사용자에게 넘긴다. 별도의 사용자 승인 snapshot이 있으면 모델 출력을
거치지 않는 `builtin.coupang.purchase-preparation@1`이 정확한 상품 identity·옵션·수량·가격 상한을
검증하고, 단일 상품의 `바로구매` 또는 여러 상품의 `장바구니 담기`와 exact cart의 `구매하기`까지만
실행한다. 주문서/checkout으로 이동하면 즉시 사용자 제어로 전환하며 최종 `결제하기`·`주문하기`는
실행하지 않는다.

password, OTP, card/payment form과 account·order·cart·profile 같은 비공개 페이지는 page data를
비우고 handoff한다. 로그인된 상태의 **공개 상품 페이지**는 계속 관찰하되, 로그아웃 control을 기준으로
찾은 계정·프로필 header/navigation subtree를 먼저 제외해 이메일·주문 링크 같은 계정 정보가 observation에
들어가지 않게 한다. 공개 상품 페이지의 buy/checkout control과 cross-origin iframe도 해당 subtree만
제외하고 `excludedBoundaryCodes`로 명시한다.

Android는 같은 regular Chromium profile을 유지하므로 사용자가 직접 로그인한 뒤 같은 세션에서 상품과
checkout 화면으로 이동할 수 있다. cookie와 저장소는 기기 profile 밖으로 내보내지 않는다. 현재 native
승인 UI는 열린 쿠팡 상품 1개의 `바로구매` 경로만 연결한다. multi recipe는 core/protocol/page-agent에
있지만 native plan UI와 Device Channel이 아직 없다. 계정·결제 화면과 최종 결제는 사용자 제어이며,
dedicated profile 분리와 운영용 profile 복구는 native build 이후 별도 검증 관문이다.

신뢰 경계와 구현 범위는 이 문서의 구조·현재 상태·검증 절을 공개 스냅샷의 기준으로 삼는다.

## macOS 공식 Chrome에서 single-item 검수

APK나 iOS binary 없이 실제 쿠팡 로그인 session에서 고정 recipe를 검수하려면 다음 로컬 harness를
사용한다. 사용자가 공식 Chrome에서 직접 로그인하고 exact 상품을 연 뒤, 별도 control 화면에서 상품
ID·옵션·수량·가격 상한을 승인해야 한다. harness는 `바로구매`를 최대 한 번 활성화한 즉시 분리하며
주문서·결제 화면은 관찰하거나 조작하지 않는다.

```bash
cd browser-fork
npm run try:coupang
```

개인 Chrome profile을 읽거나 복사하지 않고 저장소 밖의 Vitlane 전용 profile을 사용한다. 쿠팡
실사이트 DOM은 아직 승인 계정으로 검증하지 않았으므로 현재 결과는 source와 synthetic Chrome 검증이다.
실행 절차와 실패 경계는 [`docs/live-coupang-mac.md`](docs/live-coupang-mac.md)를 따른다.

## 개발 bridge 실행

Node.js 22 이상이 필요합니다.

```bash
cd browser-fork
npm ci
cp .env.example .env
node -e "console.log(require('crypto').randomBytes(32).toString('hex'))"
PLAYWRIGHT_CHANNEL=chrome npm start
```

`.env`에 다음 server-only 값을 설정합니다.

- `MODEL_NAME`: 사용하는 OpenAI-compatible model identifier
- `MODEL_BASE_URL`: 기본 `https://api.openai.com/v1`
- `MODEL_API_KEY`: browser 전용 model key. 비우면 monorepo의 `MANAGED_OPENAI_API_SECRET` 사용
- `BRIDGE_TOKEN`: native bearer이자 protocol v1 command permit의 pairing HMAC key
- `PLAYWRIGHT_CHANNEL`: 서버 측 discovery에 사용할 설치된 browser channel. 현재 개발 환경에는 Playwright
  bundled Chromium binary가 없으므로 `chrome`으로 실행

`MODEL_*`, `MANAGED_OPENAI_API_SECRET`과 `BRIDGE_TOKEN`은 APK, page, RN public env와 저장소에 넣지
않는다. 현재 bridge는 loopback에만 bind한다. 원격 개발 연결은 별도 TLS reverse proxy와 device별
pairing을 전제로 하며 이 Node bridge를 운영 run source of truth로 쓰지 않는다.

### 서버 측 판매처 discovery

trusted native client는 `/v1/step`과 같은 bearer 인증, browser-origin 거부, request size와 동시 실행
제한을 거쳐 다음 요청을 보낼 수 있다. body는 아래 네 key만 허용한다. UTF-8 query는 최대 300 bytes,
`limit`은 1–20, `offset`은 0–400이다.

```http
POST /v1/discover
Authorization: Bearer <BRIDGE_TOKEN>
Content-Type: application/json

{"query":"러닝화","source":"MUSINSA","limit":5,"offset":0}
```

`source`는 `COUPANG`, `ELEVENST`, `MUSINSA`, `KURLY`, `LOTTEON`, `DAISOMALL`, `AUCTION`,
`OHOUSE` 중 하나다. 정상적으로 후보를 찾으면
다음 형태를 반환한다.

Go CandidatePool route는 GET-only 실사이트 smoke를 통과한 `ELEVENST`, `MUSINSA`, `KURLY`를 사용한다.
`offset`과 Server가 전달한 기존 identity 집합으로 Research Again이 이미 저장한 상위 상품을 반복하지
않는다. 쿠팡·LOTTEON·DAISOMALL entry는 검색 URL·원본 상품 URL 형태를 고정하는
검토된 registry이지만 현재 strict GET-only 실행은 각각 HTTP 403 또는 상품 anchor 미렌더로
`FAILED`·`EMPTY`를 반환한다. 이 세 source는 접근 제한을 우회하지 않고 Go CandidatePool에서
활성화하지 않는다. 옥션은 검색
결과의 HTTP 상품 href를 identity로만 읽고 정확한 HTTPS URL로 재생성한다. 오늘의집은
`store.ohou.se/goods/{id}`를 `ohou.se/productions/{id}/selling`으로 재생성한다. 두 경로는 별도
GET-only 실사에서 상품 link를 확인했지만 후속 재시도에서 403도 관찰되어 지속 가용성은
아직 입증되지 않았고, Go route와도 연결하지 않았다.

```json
{
  "protocolVersion": 1,
  "results": [
    {
      "source": "MUSINSA",
      "url": "https://www.musinsa.com/products/1234567",
      "title": "상품명",
      "context": "검색 결과에서 정제한 문맥",
      "priceText": "12900원"
    }
  ],
  "coverage": { "source": "MUSINSA", "status": "SUCCEEDED", "count": 1 }
}
```

`priceText`는 선택 필드이며 한 개의 명시적인 `₩…` 또는 `…원` token만 담고 최대 64자다. `title`은
최대 300자, `context`는 최대 1,000자이며 URL은 registry가 허용한 canonical product URL만 남는다.
후보가 없으면 `EMPTY`, 검색 실행이 실패하면 `FAILED`와 안전한 `reasonCode`를 반환하며
둘 다 빈 `results`와 `count: 0`을 사용한다. 수용된 discovery 요청의 판매처 실패는 이 안정된 coverage
형태로 HTTP 200을 반환하고, 요청 자체의 인증·형식·값 오류는 해당 4xx 응답을 반환한다.

각 요청은 persistent profile이나 cookie를 재사용하지 않는 새 headless browser context를 만든다.
network route는 GET만 허용하고, service worker, WebSocket, popup과 download를 차단하며 HTTPS 요청의
DNS 결과가 private/reserved address이면 중단한다. 계속하는 request에서도 `Referer`를 제거해 검색어가
subresource로 전달되지 않게 한다. 이 검사는 route가 계속되기 전의 DNS preflight라서
검사 뒤 Chromium이 별도로 주소를 resolve/connect하는 사이의 DNS rebinding 또는 TOCTOU까지 제거하지
못한다. 따라서 이 로컬 discovery를 redirect마다 주소를 pin하는 native network policy의 대체물로 보지
않는다.

## 검증

```bash
npm ci
npm test
PLAYWRIGHT_CHANNEL=chrome npm run test:browser
npm run check:patch
bash -n scripts/build-android.sh
```

Playwright test는 새 임시 profile과 가상 판매처만 사용합니다. bundled Chromium이 설치된 CI에서는
`npm run test:browser`로 실행합니다. 로컬에서는 설치된 Chrome channel을 사용했습니다. 상세 결과와
미검증 항목은 [`docs/verification.md`](docs/verification.md)에 있습니다. 자동 Discovery test는 injected
fake browser/DNS와 fake discovery만 사용하며, 별도의 수동 live smoke 결과는 검증 문서에 기록합니다.

## Android APK 빌드

공식 build는 x86-64 Linux, 16GB 이상 RAM과 150GB 이상 여유 SSD를 권장합니다.

```bash
cd browser-fork
python3 scripts/chromium_patch.py --check
bash scripts/build-android.sh
```

성공 시 `out/VitlaneBrowser-arm64.apk`를 만듭니다. 루트의 수동
`browser-fork-android.yaml` workflow는 `self-hosted, linux, x64, chromium` runner가 있을 때만
실행됩니다. 현재 Mac의 Docker 경로는 사용자가 중지했으며 이 작업에서 재시작하지 않았습니다.
자원과 실기기 검증 항목은 [`docs/vm-build.md`](docs/vm-build.md)에 있습니다.

`chrome_public_apk`는 초기 integration target입니다. 배포용 package ID, 브랜딩, release signing,
third-party notice와 upstream security update pipeline은 별도 관문입니다.
