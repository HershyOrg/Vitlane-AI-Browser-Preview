# 검증 기록

실행 환경: Apple Silicon macOS, Node.js 23.7.0, Python 3, 설치된 Google Chrome.

최종 자동 검증 집계(2026-09-25): `npm test` unit **76/76**, desktop Chrome page-agent
**18/18**, 고정 upstream의 **10개 patch 파일** 적용 검증이 통과했다. 같은 변경의 mobile Jest는
**187/187**, Go BrowserRun 대상 test도 통과했다. Chromium 154.0.8037.49
(`ba187acfd59909f7d8d4fe0b5e9faa3a71ff0e92`) Android build는 macOS host preflight에서 x86-64 Linux를
요구하며 종료되어 native compile은 `NOT RUN`이고 APK도 없다. iOS는 Swift policy smoke
compile/execute와 전체 Swift parse가 통과했다. Expo prebuild·CocoaPods·Xcode/Simulator/device binary
build는 `NOT RUN`이다.

자동 `POST /v1/discover` 검증은 injected fake discovery, Playwright와 DNS를 사용한다. 별도 수동 smoke는
설치된 Chrome으로 실판매처 공개 검색을 호출했다.

- 11번가: offset 0/2에서 서로 다른 canonical 상품 2개씩 `SUCCEEDED`
- 무신사: offset 0/2에서 서로 다른 canonical 상품 2개씩 `SUCCEEDED`
- 컬리: offset 0/2에서 서로 다른 canonical 상품 2개씩 `SUCCEEDED`; 판매가와 상품명 정제 확인
- 롯데온·다이소몰: GET-only 정책에서 CSR 상품 링크가 렌더되지 않아 `EMPTY`; Go browser route 비활성
- Go live 흐름: FOOD 조사에서 browser provenance 후보 5개, 후속 요청에서 기존 identity를 제외한 새
  browser 후보 5개가 CandidatePool에 추가됨

## Linux VM 빌드 시도 (2026-09-17)

- Docker Desktop Linux VM + Rosetta에서 Ubuntu 22.04 amd64와 의존성 다운로드까지 확인했지만
  네이티브 compile 전에 사용자가 직접 중지했습니다.
- 사용자가 여기서 멈추라고 요청했으므로 Docker를 다시 시작하지 않았고, 이 작업에서는 이전 volume이나
  container의 현재 상태도 재검증하지 않았습니다.
- 이 시도에서 APK나 네이티브 compile 증거는 만들어지지 않았습니다. 다음 검증은 Docker 재개가 아니라
  자원이 충분한 전용 x86-64 Linux runner를 기준으로 합니다.

환경과 새 build host 절차는 [Linux 빌드 문서](vm-build.md)를 참고하세요.

## 검증한 항목

- `npm test`: 프로토콜·브리지·모델 어댑터 테스트
  - 허용 URL 및 요소 검증, 비밀 필드 제외, 요청 크기 제한
  - 브리지 인증, 웹 Origin 거부, 동시 요청 제한, 요청 취소
  - local test HTTP server를 통한 모델 호환 요청/응답 처리, 잘못된 JSON 처리
  - `/v1/discover`의 exact request schema, 8개 registry source, offset paging, bounded result와 deterministic coverage
  - injected fake discovery로 인증·Origin 거부·공유 concurrency·disconnect 취소·안전한 실패 code 확인
  - injected fake Playwright/DNS로 요청마다 새 context, GET-only route, private/reserved address,
    `Referer` 제거와 WebSocket·popup·download 차단 확인
  - 쿠팡 purchase-preparation recipe v1의 단일 `buy_now`, 다중 line별 `add_to_cart`→exact
    `https://cart.coupang.com/cartView.pang` selected line 재검사→`start_checkout` 분기, exact offer
    identity·수량·가격 승인 binding과 checkout-ready handoff marker 확인
  - protocol이 native-approved recipe step을 모델과 분리하고 snapshot digest·cursor·현재
    origin/path/query-presence를 결속하는지 확인. issuer가 lease별 승인 ID·digest와 정확한 다음 executable
    cursor를 고정하며, checkout origin은 DOM 관찰 없이 handoff하는지도 확인
  - macOS live harness가 exact 쿠팡 URL의 중복 없는 offer identity와 option 입력을 정규화하고, 수량·단가·
    총액·5분 만료를 approval digest에 결속하는지 확인
- `PLAYWRIGHT_CHANNEL=chrome npm run test:browser`: 실제 Chrome에서 모바일 화면 크기 사용
  - isolated world에 페이지 스크립트가 덮어쓴 함수가 영향을 주지 않는지
  - 비밀번호·카드·숨김 값이 스냅샷에 없는지
  - 공개 GET search value 준비가 input/change/submit event를 발생시키지 않는지
  - 오래된 요소·스냅샷·중복 실행이 거부되는지
  - buy/checkout control·cross-origin iframe subtree만 제외되고 공개 상품 설명은 남는지
  - 로그인/account/order/cart/profile 신호에서 page content 없이 handoff하는지
  - 링크 자동 이동이 비활성화되고 사용자 handoff가 반환되는지
  - run 중 다른 document load나 URL 변경을 같은 run의 새 관찰로 이어 가지 않는 native invariant
  - 브라우저 → 실제 HTTP 브리지 → 테스트 모델 → 제한된 action 확인
  - 쿠팡 product fixture에서 exact option·수량 뒤 `buy_now`만 한 번 활성화하고 final-payment control은
    누르지 않는지 확인
  - multi fixture에서 `add_to_cart`, exact selected cart line·수량·가격 상한, `start_checkout`을 각각
    검증하고 extra line·ambiguous control·final-payment 대체를 fail-close하는지 확인
  - macOS live core가 local approval과 permit cursor 순서를 거쳐 option→검증→수량→`buy_now`를 실행하고
    `바로구매` 한 번·최종 `결제하기` 0회를 유지하는지 확인. 중복 `바로구매`와 step 사이 URL 변경은 중단
- `npm run check:patch`: 고정 upstream 파일의 SHA-256 확인, 모든 패치의
  `git apply --check` 및 실제 적용 후 예상 소스와 비교
- `bash -n scripts/build-android.sh`: 빌드 스크립트 문법

## 검증하지 못한 항목

| 항목 | 이유 | 필요한 후속 작업 |
| --- | --- | --- |
| Chromium Android 전체 컴파일·APK | `bash scripts/build-android.sh`가 macOS host preflight에서 `Chromium Android requires an x86_64 Linux build host.`로 종료; Java/native compile `NOT RUN`, APK 없음 | x86-64 Linux runner의 clean checkout에서 compile |
| 네이티브 UI·Java/JNI/C++ 연결 | source·patch 검증은 통과했지만 APK/실기기 증거는 아직 기록되지 않음 | build 결과 확정 뒤 기기에서 패널·시작·중지·백그라운드·탭 변경 검증 |
| 실제 native browser-step 모델 판단 | Device Channel로 실제 기기 observation을 전달한 실행이 없음 | 기기 channel 연결 뒤 승인된 공개 fixture에서 실제 step 요청 검증 |
| 쿠팡 구매 준비 recipe 실사이트 실행 | core·protocol·desktop/Android/iOS page-agent와 partial native source는 있으나 multi native plan UI/orchestration, Device Channel, 실제 계정·기기 DOM 실행 증거가 없음 | 승인된 테스트 계정에서 single `buy_now`와 multi `add_to_cart`→exact cart→`start_checkout`까지만 smoke |
| macOS 공식 Chrome single-item 실사이트 실행 | 전용 profile·loopback control·승인·isolated page-agent source와 synthetic Chrome은 통과했지만 승인 계정의 headed 쿠팡 DOM은 아직 실행하지 않음 | 실제 계정에서 optionless 저위험 상품으로 주문서 진입까지만 확인하고 최종 주문·결제 없이 종료 |
| 롯데온·다이소몰 browser discovery | GET-only 실행에서 상품 CSR 렌더가 되지 않아 `EMPTY` | 별도 검토된 read-only API recipe가 생기기 전 기존 provider route 유지 |
| 쿠팡 browser discovery | 현재 Chrome headless 공개 검색이 HTTP 403으로 `FAILED` | 접근 정책을 우회하지 않고 기존 OWN offer 경로 유지 |
| 옥션·오늘의집 browser discovery | 별도 strict GET-only 실사에서 상품 link·canonical identity를 확인했지만 후속 재시도에서 403도 관찰 | Go route 활성화 전 반복 live smoke와 source progress 통합 |
| discovery DNS rebinding/TOCTOU | route 전 DNS lookup 결과는 검사하지만 그 IP를 Chromium connection에 pin하지 않음 | address pinning 가능한 connector 또는 동일 정책의 outbound proxy를 구현하고 rebinding test |
| bundled discovery browser runtime | Playwright bundled Chromium binary는 설치하지 않음 | 로컬은 설치된 Chrome과 `PLAYWRIGHT_CHANNEL=chrome` 사용; 배포 runtime 별도 제공 |
| Android dedicated ephemeral profile | Android host가 아직 profile을 생성·검증하지 않음 | 별도 ephemeral profile 수명·cookie/storage 격리를 구현하고 native test |
| HTTP localhost 연결의 기기 정책 | 실제 Android 네트워크 정책 미검증 | `adb reverse` smoke 및 필요 시 HTTPS 연결 |
| 배포용 서명·업데이트 | 개발 소스 단계 | 패키지 ID·서명키·업데이트 인프라 확정 |

데스크톱 Chromium 테스트는 Android 네이티브 통합 테스트를 대체하지 않습니다.
테스트 모델은 응답을 고정한 서버이며, 실제 AI가 웹 작업에 성공했다는 증거가 아닙니다.
자동 Discovery test는 injected fake만 사용한다. 위 수동 live smoke는 익명 server-side 검색 성공의
증거이며 native device browser 동작의 증거는 아니다.

## Android smoke 시나리오

1. 일반 HTTP(S) 탭에서 AI 패널이 표시되는지 확인합니다.
2. 설정한 모델로 페이지 요약을 요청하고 실제 내용과 비교합니다.
3. 검색어 준비 후 사용자가 직접 제출하고, 새 페이지에서 다시 시작합니다.
4. 모델 요청 대기 중 중지한 뒤 늦은 응답이 실행되지 않는지 확인합니다.
5. 대상 입력을 수동 수정하거나 탭을 바꾸고 오래된 동작이 거부되는지 확인합니다.
6. 앱을 백그라운드로 보내거나 화면을 회전한 뒤 이전 작업이 실행되지 않는지 확인합니다.
7. 시크릿 탭, `chrome://`, 비밀번호·파일·카드 입력에서 제약을 확인합니다.
8. 링크 후보가 자동으로 열리지 않고 직접 조작 handoff로 전환되는지 확인합니다.
