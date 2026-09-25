# OpenAPI 공유 규격

`v1.yaml`은 Web과 Server 사이 route, field, enum, error와 response shape의 기준이다.

현재 계약의 핵심:

- Web Google 로그인, 모바일 PKCE handoff와 Account projection
- Wallet registration, ownership proof, KYC와 shipping
- immutable ShoppingPlan과 독립 Curation ID
- `PLANNING | CURATING` phase와 typed CurationAction
- SINGLE/AUTO 모두 INITIAL Run·PlanningTask·IntelligenceJob, Target/Round 0개
- Target·ShoppingSession·ResearchRound·Candidate workspace projection
- IntelligenceJob retry/cancel과 progress
- CurationSelection과 state-free CartView
- OrderSheet의 `QuoteReadiness`/`ProcurementHandling`/`ProviderNotice`, AgencyOrder와
  immutable PaymentInstruction
- AgencyOrder 기반 Payment, Procurement, Logistics와 TEST Receipt
- Procurement placement의 operator 입력(ref/source, 금액 변동 시 실지급액)과 서버의
  기본 승인액 파생·hash/시각 생성 경계
- 고객 AgencyOrder의 단일 `FINISHED` terminal view, 회수 원장의 Order/MO reference
  조회, Support의 exact `awaitingCustomerMessageId`
- 운영자 core/degraded health, L1 heartbeat, L3 host facts와 bounded 시계열,
  활성 세션 조회·사용자 범위 revoke
- 운영 work kind 전역 count와 고객 승인을 지난 exact PayPal Live 주문 navigation count
- Managed `UNKNOWN` reservation의 fresh-auth·감사된 SETTLED/RELEASED 판정
- 공통 fault envelope와 `Retry-After`
- CurationThread v2의 Job receipt: 0건 조사는 `NO_RESULTS` effect, 병렬 Job 일부
  실패는 Action·Thread `FAILED` + `reasonCode: PARTIAL_FAILURE`(성공 Job receipt 보존)
- OrderSheet exact-line 차단 오류의 stable reason과 merchant 원문 `itemTitle`

## Plan 생성 기준 shape

```text
@Intent-NextStep
-> ShoppingPlan
-> Curation(PLANNING, version=1)
-> INITIAL CurationRun
-> PlanningTask
-> CurationAction
-> IntelligenceJob
```

Target과 ResearchRound는 0개다. 첫 accepted Proposal이 Target과 READY Session을
만든다. `SINGLE`은 INITIAL Target 하나, `AUTO`는 하나 이상을 허용한다.

신규 Plan의 `agentMode` enum은 `MANAGED`만 허용한다. `EXTERNAL`은
`EXTERNAL_AGENT_RETIRED`로 거절하며 새 작업을 만들지 않는다.

## Consumer 규칙

- 브라우저는 HttpOnly `vitlane_session` cookie를, 네이티브 앱은
  `Authorization: Bearer <opaque AuthSession>`을 사용한다. 한 요청에 둘을 함께 보내지 않는다.
- 네이티브 Bearer는 Vitlane AuthSession이다. Google access/refresh token, provider token,
  MCP capability token을 `/api/v1` 인증에 사용하지 않는다.
- Plan ID와 Curation ID를 같은 값으로 보정하지 않는다.
- 가능한 행동은 `availableActions`를 따른다.
- 같은 command retry는 같은 idempotency/action ID를 재사용한다.
- CartView에 별도 lifecycle을 만들지 않는다.
- AgencyOrder 상태를 Curation phase로 투영하지 않는다.
- provider raw message/status를 Web에서 주문 상태로 재해석하지 않는다. 서버가 준
  `QuoteReadiness`, `ProcurementHandling`, notice audience/presentation을 그대로 쓴다.
- `itemTitle`은 문제 Cart line을 사용자에게 지목하는 원문 content이며 locale에 따라
  번역하거나 reason code에 합치지 않는다.
- generated type과 parser golden fixture를 schema와 같은 revision에서 갱신한다.
- 제거된 route나 외부 Agent API로 fallback하지 않는다.

OpenAPI 변경은 Server handler, Web consumer, fixture와 conformance test를 같은
변경에서 갱신한다. README 설명만 바꿔 contract 변경을 완료 처리하지 않는다.

## 모바일 AuthSession handoff

8.16.0은 시스템 브라우저의 Google 로그인을 네이티브 앱의 opaque Bearer AuthSession으로
안전하게 넘기는 PKCE S256 계약을 추가한다.

1. 앱은 43~128자의 PKCE verifier를 기기에서 만들고 보관한다. SHA-256 digest를 padding
   없는 base64url로 인코딩한 43자 `code_challenge`를 만든다.
2. 앱은 `GET /auth/mobile/google/start`를 시스템 브라우저로 열며, 배포 설정의
   `MOBILE_AUTH_REDIRECT_URIS`와 정확히 일치하는 `redirect_uri`를 함께 보낸다.
3. Google callback 뒤 Server는 단기 브라우저 AuthSession을 폐기하고, 앱 callback에는
   2분 유효·1회용 `code` 또는 stable `error`만 전달한다. 장기 AuthSession과 Google
   token은 URL에 싣지 않는다.
4. 앱은 `POST /auth/mobile/exchange`에 `code`와 원래 verifier를 한 번 보내 새
   `sessionToken`을 받는다. token은 Keychain/Keystore 같은 OS 보안 저장소에 저장하고
   URL, 로그, 분석 이벤트에 기록하지 않는다.
5. 보호된 API에는 Bearer 하나만 보내며, 로그아웃이나 만료 뒤에는 보안 저장소에서도
   token을 제거한다.

`GET /auth/capabilities`의 `mobileGoogleEnabled`가 `true`일 때만 앱이 이 진입점을
노출한다. `POST /dev/auth/session`의 `sessionMode: bearer`는 local·test 검수 전용이며
production에서는 해당 개발 route 자체가 비활성화된다.

계약 검증은 다음 명령으로 실행한다.

```sh
node --test shared/openapi/mobile-auth-contract.node-test.mjs
node --test scripts/ci/wallet-identity-kyc-openapi.test.mjs
```

## 큐레이션 증강 Step 2

8.4.0은 Account Last Select GET/PATCH, 다음 ResearchSettings GET/PATCH, 일별 환율 GET,
외부 ProductRef 자기보고, API product별 운영자 usage/control을 추가한다. `SourceProductRefV3`의
KR ProductRef와 기존 CatalogProductObservation의 `externalObservation.v1`은 original product
identity와 상품 수준 가격/UNKNOWN을 명시한다. workspace/hydration의 Source union을 양
consumer에서 함께 확장한다. `ExternalPurchaseFeedback`는 Amazon-only일 때 v3, ProductRef가
포함되면 v4이며 각 record는 VariantRef/ProductRef 배타 union이다.

공유 fixture `fixtures/curation-step2.v1.json`을 Server workspace projection과 Web Firefox
fixture에서 사용한다. 실제 API 실호출 결과는 공개 fixture에 포함하지 않는다.

## 원본 상품 반응 fallback

GET external-product에 선택적 `reactionAllowed`/`reaction`을 추가하고,
PUT external-product/reaction에 `vitlane.product-reaction.v1` CAS 계약을 정의한다.
KR 원본 관찰 및 Variant 부재가 확인된 상품에만 적용하며 기존 Variant API를 대체하지 않는다.
계정 liked-variants의 기존 `candidates`와 별도로 `products`는 저장된 원본 관찰을 전달한다.
ResearchFeedback의 열린 interactionSnapshot에는 선택적 Target-scoped `products`가 추가된다.


## 8.6.0 Curation threads

controlMode와 curation thread/decision/step/job/effect/question/answer 모델을 추가했다. 예산 primitive payload는 기존 CurationBudgetCommandV1을 재사용한다. 독립 budget/proposals는 deprecated 410이다. 상태·요청은 UI locale과 무관하고 짧은 evidence와 사용자 원문을 그대로 보존한다.

Step 7은 background-research 조회, subscription cancel, finding hide/candidates, product-notices/sync와 versioned SubscriptionTerms/DealProduct/Finding 규격을 추가한다. 생성은 기존 FollowUp 응답 API만 사용한다.
