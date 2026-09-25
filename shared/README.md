# Shared 규격

루트 `shared/`는 둘 이상의 runtime이 실제로 소비하는 versioned contract만
소유한다.

```text
shared/
├── openapi/      Web·Server HTTP schema와 fixtures
├── onchain/      consumer ABI와 deployment manifest
├── events/       cross-runtime event schema
├── receipt/      Receipt shared schema
└── brand/        Web·Marketing brand source
```

- HTTP 계약의 기준은 `openapi/v1.yaml`이다.
- AgencyOrder HTTP 계약에는 기능이 꺼져 있어도 항상 응답하는 capability와
  OrderSheet 발행부터 기존 Settlement finality 조회까지의 surface를 포함한다.
- capability v2는 PayPal Live·PayPal Sandbox·tVITUSD 상태와 runtime revision을
  독립 표현하며, 운영자 Live control v1은 credential 없는 readback과 kill만 노출한다.
- Solidity source, test와 deploy script는 `contracts/`가 소유한다.
- `onchain/`에는 consumer용 ABI와 주소 manifest만 생성한다.
- 특정 제품의 편의 타입, 단일 consumer helper와 미래 예약 폴더는 두지 않는다.
- 외부 Agent/MCP와 범용 executor shared surface는 현재 계약에 없다.

각 규격은 둘 이상의 runtime이 소비하는 versioned contract여야 하며, 제품별 편의
타입과 단일 consumer helper는 해당 consumer가 소유한다.

큐레이션 증강 Step 1의 source/ProductRef·VariantObservation·외부 구매 자기보고·operator quota는
OpenAPI v1 transport 안의 `vitlane.*.v3` payload로 정의한다. 기존 Shopify Candidate ID와 Cart payload는 유지한다.
내부 data vendor와 credential은 공유 규격에 넣지 않는다.
운영자 quota DTO의 선택 필드 `mode`는 LIVE/STUB/DISABLED다. 고객 Source와 별개인
실행 상태이며 STUB의 quota는 마지막 실조회 기록이다. 현재값으로 갱신됐다고 해석하지 않는다.
Variant 반응 PUT의 선택적 `relationToken`은 새 Amazon 임시 옵션의 서버 관계 검증용이다.
소유자·Candidate·직접 ASIN·만료를 확인하며 configuration 저장을 대체하지 않는다.
`CatalogResearchWorkspace`에는 외부 상품 카드 상태가 함께 실린다. `purchaseFeedback`,
`productReactions`, `reactionAllowedCandidateIds`와 `configurations[].version`이며 모두 DB-only 읽기다.
`GET /account/purchase-checks`(`vitlane.account-purchase-checks.v1`)는 구매 체크의 계정 사영이며
Amazon purchase-check PUT의 선택적 `snapshot`은 체크 시점 표시값이다(ADR-0075). 둘 다
주문·가격 권위가 아니고 기존 `ExternalPurchaseFeedbackV3`는 바뀌지 않는다.

Step 3의 `vitlane.curation-budget.v1`은 초기 nullable 예산 요청, 현재 원장/Target 배분,
CAS+멱등 편집 명령과 미저장 AI 제안을 공유한다. 현재 총액은 배분 합계이며 기존 Plan 금액은 provenance다.


InitialCurationBudgetV1의 optional inputMode는 AUTO/EXPLICIT를 구별한다. AUTO는 null 입력에만
허용하며 Init 자동 해석이 가능하다. 생략한 기존 caller는 EXPLICIT로 다뤄 의미를 보존한다.
