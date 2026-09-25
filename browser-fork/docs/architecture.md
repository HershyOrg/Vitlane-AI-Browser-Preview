# Vitlane 모바일 AI 브라우저 구조

## 플랫폼 경계

- Android는 고정 Chromium revision의 공개 browser app에 최소 patch를 적용한다. Android System
  WebView와 섞지 않고 Blink/V8/network/sandbox/security 동작을 자동화 편의로 바꾸지 않는다.
- iOS는 시스템 `WKWebView`와 격리된 `WKContentWorld`를 쓴다. Android와 protocol 및 공통 RN
  presentation을 공유하지만 capability 동등성을 가정하지 않는다.
- RN은 목표, 현재 작업, 필터가 허용한 데이터 범위, activity, takeover/handoff와 result를 표시한다. 실제
  origin, foreground, tab/document identity, 즉시 정지와 실행 권한은 native host가 소유한다.

## 한 행동 폐루프

```text
native page identity + fixed page agent
  → SanitizedObservation
  → bridge schema/privacy validation
  → model proposes one high-level action
  → deterministic server policy
  → actionHash + short expiry + HMAC serverPermit
  → native permit/binding/generation/page recheck
  → isolated-world inspect
  → journal STARTED
  → one fixed action
  → ActionResult + fresh observation
```

`/v1/step`과 `/v1/discover` request/response의 source of truth는 `protocol/v1.schema.json`이다. JavaScript canonicalization은
object key를 Unicode 코드 단위 순서로 정렬하고 공백 없는 JSON을 만든다. `actionHash`는 canonical
`action`의 SHA-256 hex이고 `serverPermit`은 permit을 제외한 command의 canonical JSON을
HMAC-SHA256한 base64url 값이다. `protocol/fixtures`의 request, command, result를 다른 consumer의
conformance 기준으로 사용하며 discovery request/response에도 별도 golden fixture가 있다.

현재 Node bridge는 개발 adapter이며 state를 메모리에 둔다. production에서는 device 인증, lease,
durable journal, outbox와 run state를 기존 Go/PostgreSQL 경계로 옮겨야 한다.

## 서버 측 로컬 discovery

`POST /v1/discover`는 위의 native 한 행동 폐루프와 분리된 후보 검색 경로다.

```text
trusted client
  → bridge bearer/origin/request-size/concurrency gate
  → exact { query, source, limit, offset } validation
  → fixed merchant registry
  → fresh Playwright browser context
  → GET-only public-network fetch
  → canonical URL + bounded title/context/priceText
  → deterministic results + coverage
```

지원 source는 `COUPANG`, `ELEVENST`, `MUSINSA`, `KURLY`, `LOTTEON`, `DAISOMALL`, `AUCTION`,
`OHOUSE`로 고정한다. 요청은 최대 300 UTF-8
bytes의 공개 검색어, source, 1–20의 limit과 0–400의 offset만 받는다. 응답은 `protocolVersion: 1`,
`results`와 source별 `coverage`를 가지며
각 result는 `source`, canonical product `url`, `title`, 최대 1,000자의 `context`를 포함한다. 한 개의
명시적인 원화 token을 찾은 경우에만 최대 64자의 `priceText`를 추가한다. Coverage status는
`SUCCEEDED`, `EMPTY`, `FAILED` 중 하나이고 실패 시 외부 URL이나 query를 노출하지 않는 고정
`reasonCode`만 추가한다.

Go의 CandidatePool adapter는 live GET-only 검색이 확인된 11번가·무신사·컬리만 browser route로
선택한다. 저장된 browser 후보 수와 source progress 중 큰 값을 offset으로 사용하고, 기존 durable
identity는 결과에서 다시 제외한다. 따라서 후속 계획의 fingerprint가 바뀌어도 첫 묶음을 반복하지
않는다. 쿠팡·롯데온·다이소몰은 검토된 검색 및 원본 상품 URL registry와 canonicalizer만
제공한다. 옥션·오늘의집은 strict GET-only 실사에서 상품 link까지 확인했지만 이 변경은
browser-fork 계약만 늘리므로 Go browser route에서는 아직 활성화하지 않는다.

이 route는 `/v1/step`과 같은 bearer 인증, website `Origin`/`Sec-Fetch-Site` 거부, request size,
동시 실행 budget, bridge timeout과 client disconnect 취소를 사용한다. Discovery 결과에는
`serverPermit`이 없고 native tab/document identity나 action generation에 bind되지 않는다. Candidate를
자동으로 device browser에서
열거나 native journal에 기록하지도 않는다. 따라서 server-side 검색 결과를 Android Chromium/iOS
WebKit의 탐색 또는 행동 실행 증거로 취급하지 않는다.

각 discovery는 cookie나 persistent profile을 공유하지 않는 새 headless Playwright browser/context를
열고 종료한다. `acceptDownloads: false`, service worker 차단, WebSocket 종료, popup 닫기와 download
취소를 적용하고 network request는 GET만 계속한다. HTTPS/hostname 검사와 DNS lookup 결과의
private/reserved address 차단을 각 route 전에 수행하고 top-level document는 선택한 registry의 판매처로
제한한다. 계속하는 request에서는 query가 subresource에 `Referer`로 전달되지 않도록 해당 header를
제거한다.

DNS 검사는 Chromium connection에 검사한 IP를 pin하지 않는다. 검사와 Chromium의 독립적인
resolution/connect 사이에 DNS 응답이 바뀌는 rebinding/TOCTOU 가능성이 남는다. 운영 수준에서는
resolve 결과를 실제 connection에 고정하거나 동일한 정책을 가진 outbound proxy를 사용해야 한다.
현재 route는 그 보장을 제공한다고 주장하지 않는다.

## 관찰

page agent는 page 전역과 분리된 isolated world의 앱 내장 script다. 다음 정보만 구조화한다.

- native가 공급한 tab/frame/document/origin/foreground metadata
- bounded title/visible text
- 같은 origin이고 query/fragment가 없는 공개 HTTP(S) link candidate
- 값이 비어 있고 GET form에 속한 public search field candidate
- 수집 상태, 전체 차단 `handoffReasonCodes`, subtree 제외 `excludedBoundaryCodes`

input value, hidden text, password·OTP·card, URL query/fragment, cookie, storage, network body와
screenshot은 observation에 넣지 않는다. password·OTP·payment input/form, 이미 값이 있는 input,
현재 URL·제목·상단 heading·logout control의 로그인/account/order/cart/profile 신호는 page text와
candidates를 비우고 사람에게 넘긴다. 공개 상품 페이지의 buy/checkout control과 cross-origin iframe은
해당 subtree만 text/candidate에서 제외하고 `excludedBoundaryCodes`로 선언한다.

DOM은 계속 신뢰하지 않는다. candidate reference는 observation 한 번에만 유효하고 element 연결,
visibility, fingerprint, URL과 page identity를 실행 직전에 다시 검사한다.

## 허용 action

| action | 동작 |
|---|---|
| `inspect_page` | 실행 없이 새 관찰 요구 |
| `open_candidate` | redirect/DNS-aware native navigation policy가 없어 자동 이동하지 않고 사용자에게 handoff |
| `scroll` | 현재 viewport의 위/아래 이동 |
| `run_preparation_step` | `builtin.public-search@1/prepare_query`와 native 승인 snapshot에 결속된 `builtin.coupang.purchase-preparation@1`의 `select_option`·`verify_options`·`set_quantity`·`buy_now`·`add_to_cart`·`start_checkout`만 허용 |
| `request_human` | page mutation 없이 handoff |
| `finish` | local 완료 메시지. 거래 완료 증거가 아님 |

검색어에는 URL, 이메일, 긴 번호와 secret/password/OTP/card 의미를 허용하지 않는다. prepared value는
다음 관찰에서 읽지 않고 sensitive-input handoff로 전환한다. 범용 click/fill/eval은 protocol에 없다.

## 판매처 구매 준비 레시피 코어

`server/purchase-recipes.mjs`는 모델이 버튼 이름이나 구매 경로를 즉석에서 만들지 않도록 판매처별
경로를 버전으로 고정한다. 첫 registry인 `builtin.coupang.purchase-preparation@1`은 `search → product
→ options` 뒤 한 상품이면 `buy_now`, 여러 상품이면 각 line의 `add_to_cart`를 마친 뒤 exact selected
cart URL `https://cart.coupang.com/cartView.pang`에서 line·수량·가격 상한을 다시 확인하고
`start_checkout`을 한 번 실행한다. 버튼은 CSS selector가
아니라 role, accessible name, option group을 모두 만족하는 단 하나의 fresh semantic candidate에만
연결한다. 없거나 비활성화됐거나 둘 이상이면 generic click으로 대체하지 않고 중단한다.

승인 snapshot은 `approvalId`, `sha256:` digest, revision, 10분 이내 expiry, KRW 총액 상한과 각 line의
`productId + itemId + vendorItemId`, 수량, 단가·line 상한을 묶는다. 상품 URL도 이 세 ID를 보존한다.
여러 상품의 cart 단계는 현재 선택된 line이 승인 목록과 정확히 같고 각 가격 상한 안일 때만
`구매하기`, `구매하기(2)` 같은 cart CTA를 준비할 수 있다. 선택된 다른 상품이 하나라도 있으면
`CART_SELECTION_MISMATCH`로 닫힌다.

recipe core의 마지막 `checkout_ready`는 비변경 handoff marker이며 protocol의 실행 step이 아니다.
`checkout.coupang.com`은 native/page-agent에서 민감 화면으로 분류해 관찰하거나 실행하지 않고 즉시
사용자 제어로 넘긴다. 따라서 인증된 checkout pathname이나 heading을 automation 권한으로 사용하지
않는다. recipe에는 payment, place-order, purchase/order confirmation action이 없고 로그인·password·
OTP·CAPTCHA와 최종 주문·결제는 항상 사용자가 처리한다.

현재 연결은 source/fixture 수준에서 다음까지 구현됐다.

- protocol runtime은 native가 승인한 snapshot·digest·cursor와 browser가 확인한
  origin·pathname·query/fragment 존재 여부를 다시 검사하고, 쿠팡 step을 모델 출력과 분리해 고정 action으로
  발급한다. command issuer는 lease별 승인 ID·digest·mode와 정확한 다음 executable cursor를 고정한다.
- desktop/Android isolated page agent와 iOS bundled page agent는 exact option·수량·offer/cart line을
  다시 확인한 뒤 위의 여섯 executable step만 처리한다. `buy_now`와 `start_checkout`은 실행 직후
  handoff하고 checkout host에서는 후속 관찰이나 action을 만들지 않는다.
- Android coordinator source는 현재 상품에서 single-item approval을 만들고 protocol/page agent까지
  전달한다. multi-item approval UI·전 route orchestration은 연결되지 않았고 activation 뒤 사용자에게
  넘긴다.
- iOS Expo module source는 TypeScript/Swift command validation과 isolated-world executor까지 연결한다.
  Swift가 승인 snapshot digest를 canonical JSON으로 재계산하고, 같은 승인 안의 executable step 순서를
  로컬에서도 고정한다. 결과가 불명확하면 해당 순서를 잠가 새 lease/승인 없이 재시도하지 않는다.
  공통 RN Server viewport는 아직 이 execute API에 Device Channel command를 공급하지 않고, handoff 뒤
  fresh public-product observation을 확인하는 one-shot gate만 사용한다.

따라서 core→protocol→page-agent→native dispatch의 일부 source 경로는 존재하지만, Android/iOS의
완전한 single/multi run, 실제 계정 session과 live 쿠팡 DOM 실행을 증명하지 않는다.

## Android patch

`ChromeTabbedActivity`에 native coordinator를 붙이고 `WebContents`에 Vitlane 전용 isolated-world
평가 메서드와 고정 world ID를 추가한다. 호출자가 world ID를 선택할 수 없고 C++는 항상
Chrome의 `ISOLATED_WORLD_ID_LANE_AGENT`에 대응하는 고정 slot만 사용한다. 외부 remote-debugging
port와 page Java interface는 열지 않는다. coordinator는 현재 탭/WebContents, native origin,
foreground, document generation과 control generation을 바인딩한다. background, tab/content 변경,
다른 document load 또는 URL 변경, stop과 takeover는 즉시 대기 network·command를 폐기한다. run 중
navigation 뒤 새 페이지를 같은 run에서 다시 관찰하지 않는다.

native compile 전 source API가 pinned Chromium과 맞는지는 확정하지 않는다. patch generator는
upstream 파일 SHA-256, diff 재생성과 `git apply --check`만 증명한다.

현재 Android host는 dedicated ephemeral profile을 생성·검증하지 않았다. regular persistent profile의
인증 상태를 유지하므로 격리했다고 주장하지 않으며, page sanitizer가 로그인/account 신호를 찾으면
fail closed한다. local panel의 쿠팡 승인은 single-item `buy_now` 경로만 구성한다. coordinator와
isolated page agent에는 multi step 검증 source도 있지만 Device Channel이나 공통 RN run과 연결된
multi-item 실행 경로는 없다.

## iOS adapter

`mobile/modules/vitlane-browser`는 Apple-only Expo local module이다. native security/control bar가
`WKWebView.url`에서 origin을 표시하고 stop/takeover를 처리한다. 앱에 포함된 script만 named
`WKContentWorld`에서 실행한다. RN에 임의 JavaScript 평가, cookie와 raw DOM API를 노출하지 않는다.
navigation/lifecycle은 observation reference를 폐기하며 민감 화면은 사람에게 넘긴다. 같은 mounted
view가 앱 범위 `WKWebsiteDataStore.default()`를 사용해 로그인·상품 검토·checkout handoff 동안 session을
유지한다. cookie와 storage는 앱 밖이나 RN/model로 내보내지 않는다. `profileRef`별로 분리된 여러
판매처 profile은 아직 지원하지 않는다.

## macOS 공식 Chrome live harness

`scripts/live-coupang-single.mjs`는 APK·iOS binary가 없는 개발 Mac에서 single-item recipe를 실제
headed Chrome에 연결해 검수하는 별도 로컬 경로다. 개인 Chrome profile을 읽거나 복사하지 않고
별도 `user-data-dir`로 공식 Chrome을 시작한다. control server와 Chrome CDP는 모두
`127.0.0.1`의 임의 port에만 bind하고, control mutation은 run마다 생성한 token을 custom header에
요구한다. foreign Origin·Host와 cross-site request에는 CORS 권한을 주지 않는다.

사용자가 Chrome에서 직접 로그인하고 정확한 상품 탭을 연 뒤에만 URL metadata로
`productId + itemId + vendorItemId`를 확인한다. 로그인/account/cart/checkout 등 다른 탭의 DOM은
관찰하지 않는다. control 화면은 수량, 단가·총액 상한과 exact option group/value를 보여 준 뒤 별도
승인을 받는다. 그 snapshot을 수정하지 않고 `validateStep → createCommandIssuer.authorize →
verifyAuthorizedCommand → isolated page-agent` 순서로 전달한다.

실행 중에는 각 cursor 전에 승인 당시의 전체 상품 URL을 다시 비교하고, expected result code가 아니면
다음 cursor로 가지 않는다. `buy_now`는 시도 전에 one-shot 상태로 잠그므로 navigation 중 결과가
사라져도 재시도하지 않는다. 해당 isolated page CDP session은 시도 직후 분리하며 checkout DOM을 새로
관찰하지 않는다. 최종 주문·결제 action은 harness에도 없다.

이 경로의 unit·synthetic Chrome 검증은 native build 증거가 아니다. fresh headless Chrome의 쿠팡
요청은 403이므로 접근 제한을 우회하지 않으며, 승인 계정의 headed 실사이트 smoke도 아직 별도 관문이다.

## 남은 보안 관문

문자열 hostname 정책이나 server discovery의 DNS preflight만으로 DNS rebinding과 redirect의 실제
resolved IP를 보장할 수 없다. native navigation policy와 network stack에서 redirect마다 public
destination을 검사하고 navigation 뒤 새 observation을 요구해야 한다. cross-origin iframe/OOPIF,
popup/new tab, renderer crash, device key
provisioning과 journal crash recovery도 native test가 필요하다.

`checkout.coupang.com`을 포함한 checkout host, 최종 결제·예약·취소 확정, 로그인, OTP, CAPTCHA와
결과가 불명확한 외부 효과는 사용자가 직접 처리한다. click 반환, URL 변경 또는 앱 복귀만으로 완료
상태를 만들지 않는다. Device Channel·APK·live merchant 실행 증거는 아직 없다.
