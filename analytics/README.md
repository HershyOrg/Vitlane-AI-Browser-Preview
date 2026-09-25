# 사용자 분석 규격 v1

## 기록의 분리

필수 업무 기록은 서비스 제공·상태 복구·승인·거래 증빙을 위해 이미 저장하는 PostgreSQL
기록이다. 이 변경에서 기록 종류, 보존 기간, 사용자 요청 본문 저장량을 늘리지 않는다.
`sql/`은 권한 있는 운영자가 기존 기록에서 집계한다. 추가 분석 동의는 참조하지 않는다.
새로운 행동을 단순히 필수 기록이라고 분류해 수집하는 기능은 없다.

추가 행동 분석은 Web에서 명시적으로 동의한 이후만 수집한다. 미선택·거부·철회는
GA 스크립트 로드 및 이후 이벤트 전송을 차단한다. 자동 클릭 수집, 전역 클릭 listener,
모든 Button 속성, 세션 리플레이, 사용자 입력·상품명·검색어·DOM 추출은 없다.

선택과 안내 일정은 분리한다. 미선택 상태는 선택하거나 ×로 닫을 때까지 안내를 유지한다.
×는 현재 동의를 보존하고 미허용 시 24시간 안내를 미룬다.
거부·철회한 방문자에게 마지막 안내/거부 후 24시간 간격으로 다시 안내하지만
기존 거부와 수집 차단은 유지한다. 별도 180일 cookie `vt_analytics_notice`의
시각은 UI 일정용이며 행동 이벤트·업무 기록으로 저장하거나 GA4 필드에 넣지 않는다.

## 이벤트

| 이름 | 발생 지점 | 의미 |
| --- | --- | --- |
| page_view | 허용된 화면 경로 | ID·쿼리 없는 화면 종류 |
| login | 동의 후 Google 로그인 시작 → 사용자 확인 | 세션 갱신은 제외 |
| research_results_viewed | 후보 grid가 화면에 보임 | 조회 가능해진 결과 묶음 |
| candidate_viewed | 상세 패널이 보임 | 상품 상세 조회 |
| external_merchant_opened | 상세/카드의 명시적 외부 상품 이동 | 구매 성공이 아님 |
| order_sheet_viewed | 주문서가 로드됨 | 발행·결제 아님 |
| curation_created | 최초 큐레이션 생성 성공 | 재실행 제외 |
| candidate_reacted | 상품/옵션 반응 저장 성공 | LIKE/DISLIKE/NONE 상태 쓰기 |
| cart_updated | CatalogCart Replace 성공 | SET, 전체 cart 경로 이력 아님 |
| external_purchase_reported | 외부 구매 체크 저장 성공 | REPORTED/RETRACTED 자기 신고 |
| agency_order_issued | 주문서 발행 transaction 성공 | 결제 완료 아님 |

공통 `schema_version=1`, `event_id`, `surface`, `emitter`, `ui_locale`, `release`.
GA4 전송에는 같은 사건 식별자를 `event_key`로 함께 보낸다. 실제 gtag.js가 `event_id`를
전송에서 생략하는 것을 확인했으므로 export용 이름을 분리한다. 새 ID나 행동을 만드는 것이 아니다.
BigQuery는 `event_key`를 우선하고 이전 서버 packet의 `event_id`를 fallback으로 사용한다.
기존 필드를 제거하거나 schema version 1의 기존 consumer를 깨뜨리지 않는 추가 필드다.
허용 source는 SHOPIFY/AMAZON/COUPANG/ELEVENST, action은 코드의 닫힌 목록이다.
GA4 user_id는 서버 HMAC 가명이며 원본 계정 ID·이메일·지갑을 전송하지 않는다.
서버 이벤트의 curation_key/event_id/event_key도 HMAC. ID는 GA 커스텀 차원에 등록하지 않는다.
가명은 익명 데이터가 아니며 삭제·접근 통제를 적용한다.

## 집계 정의

- 업무 활동 계정: 기간 내 큐레이션 생성, 대화 요청, 주문 발행 중 하나가 있는 계정.
  조회만 하는 계정·다른 업무 쓰기는 포함하지 않는다. 전체 서비스 MAU라고 부르지 않는다.
- 월간 업무 활동: 해당 달 KST 경계로 business-usage 실행.
- 관측 활성 계정: 동의한 GA4 user_id의 규정된 유의미한 행동.
  usage.sql에 30일 범위를 넣으면 관측 MAU. GA4 화면의 Active users와 다른 정의다.
- 내부/행동 코호트: 각각 최초 남아 있는 업무 활동/최초 관측 유의미한 행동일.
  W1은 7~13일, D30–59는 30~59일. 전체 관측 기간을 채운 사용자만 분모에 넣는다.
  서비스 가입일 리텐션, 신규 가입자 코호트라고 부르지 않는다.
- 관측 범위: 동일 기간의 observed_curations_created / curations_created,
  observed_agency_orders_issued / agency_orders_issued를 나란히 본다.
  이는 동의율이 아니라 거부·차단기·SDK 지연·전송 실패·대상 제외를 합친 수집 범위다.
  이를 전 사용자로 보정하거나 거부자의 페르소나를 추론하지 않는다.
- PayPal capture, 환불 완료, 발행 주문, 현재 PLACED merchant order는 분리한다.
  LIVE/SANDBOX/TESTNET, 경제 효과·판매처 실행 모드·통화가 다른 금액을 합치지 않는다.
  이 쿼리는 회계 원장·모든 과거 결제 rail 보고서를 대체하지 않는다.
- SQL 입력에서 운영/테스트 계정을 동일한 UUID 목록으로 제외한다. 계정 ID나 원문은 출력하지 않는다.

GA4 BigQuery는 daily export 시작 이후 원시 이벤트만 사용한다. 최신 3일은 지연 도착으로
변할 수 있으므로 확정 비교는 3일 지연 cutoff를 쓴다. BigQuery SQL은 실제 프로젝트에서
dry run과 샘플 비교 후 사용한다. 동의 전 행동은 재생하지 않는다.
화면 once-key는 메모리에만 존재하며 UI StrictMode 중복을 막는다. 서버 stable event_id는
BigQuery에서 중복 제거한다. 버전 없는 옵션 PUT은 저장 요청 단위로 기록하므로 재시도에
의한 과대계수가 가능하다. 정확한 상태 변화 횟수라고 해석하지 않는다.

## 확장 및 제외

새 기능 개발 때 일반 클릭을 추가할 필요가 없다. 제품 질문에 필요한 사건만 계약에
추가하고 허용 필드·성공 시점·검증을 함께 작성한다.
iOS는 이번 변경 대상이 아니다. 향후 Firebase SDK adapter가 동일한 사건 의미·동의·가명
규격을 구현할 수 있으나 웹 구현을 그대로 이식하는 것은 아니다.

AI는 집계 SQL 결과와 Search Console 보고서로 가설을 만들 수 있다. 원문 사용자 데이터나
개별 사용자별 검색어 연결 기능은 만들지 않는다. 페르소나는 관측 표본의 행동 가설이다.
