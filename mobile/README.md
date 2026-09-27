# Vitlane mobile

Expo SDK 57의 iOS·Android 공통 앱이다. 기본 화면은 별도 Vitlane 서버 없이 OpenAI
Responses API에 직접 연결하는 개인용 대화 앱이다. Android 0.8.24는 일반 대화와 **AI 브라우저**를 하나의 채팅 화면에서 사용한다.
설치 후 **OpenAI 연결**에서 본인의
API 키를 입력하면 된다. 키와 모델 설정은 기기의 SecureStore에 저장하며 APK나
`EXPO_PUBLIC_*`에 API 키를 포함하지 않는다. 기본 모델은 `gpt-6-sol`이며 설정에서
변경할 수 있다. 대화는 메모리에만 유지하고 API 요청에는 `store: false`를 사용한다.
대화와 작업 기억은 앱 재시작 또는 새 대화에서 사라진다. 연락·배송 정보와 사용자가 저장하기로 한 비밀번호·OTP·카드 정보, 각 사용 기록만 암호화된 로컬 DB에 남는다.

연동 명세: [OpenAI 텍스트 생성 문서](https://developers.openai.com/api/docs/guides/text).
각 요청은 현재 대화를 전달하며 120초 제한을 사용한다. `gpt-6-sol` 요청은 추론 강도 `medium`,
추론과 응답을 합한 최대 생성량 16,384토큰을 사용한다. 다른 모델에는 기존 4,096토큰 한도를
유지하며 Sol 전용 추론 설정을 보내지 않는다. 네트워크
실패나 취소 시 자동 재전송하지 않는다. Android에서는 보낸 메시지가 채팅에 남고 사용자가 다시 보내 재시도한다. iOS·웹에서는 입력 내용을 복원한다.

0.5.2에서 대화와 Android AI 브라우저의 기본 모델을 함께 변경했다.
구버전에 저장한 `gpt-5-mini`는 설정을 처음 읽을 때 `gpt-6-sol`로 한 번 전환하며 API 키와
브라우저 실행 한도를 유지한다. 그 뒤 사용자가 직접 선택한 모델과 기존의 다른 모델 설정은 유지한다.
모델 접근 권한은 사용자의 OpenAI 프로젝트에 따라 달라진다.
[GPT-6 Sol 공식 명세](https://developers.openai.com/api/docs/models/gpt-6-sol)를 기준으로 반영했으며,
실제 API 계정으로 수행하는 품질·응답 시간 검증은 별도로 필요하다.

0.8.9에서는 먼저 `gpt-6-sol` 추론 라우터가 요청을 일반 대화와 웹 작업으로 분류한다. 웹 작업은
브라우저를 별도로 켜지 않아도 시작한다. 상대 날짜는 기기의 현재 시각과 함께 모델에 전달한다.
검색 공간 자체를 바꾸는 필수 조건이 빠졌을 때에는 검색 전에 한 가지 질문만 한다. 예를 들어 구매할 자켓의
남성용·여성용·공용 구분이 없으면 채팅 선택지로 먼저 확인한다. 가격대처럼 후보를 본 뒤 정해도 되는 선호나
대화·현재 시각으로 알 수 있는 날짜는 다시 묻지 않고 가능한 후보를 먼저 조사한다. 후보는 가로 스크롤
카탈로그 카드로 재사용해 표시한다.

웹 작업으로 분류되면 브라우저를 조작하기 전에 Responses API의 [`web_search` 도구](https://developers.openai.com/api/docs/guides/tools-web-search)를 반드시 호출한다.
리서치 플래너는 검색 출처에 실제로 포함된 공개 URL만 사용해 진입 URL·대안 URL·예상 화면 단계·로그인 가능성·
동적 위험과 요청 조건을 포함한 실행 그래프를 만든다. 단순 클릭 수가 아니라 행동 수, 실패 가능성, 모델 호출,
페이지 로드, 세션 선행조건과 되돌릴 수 없는 동작의 위험을 함께 고려해 **shortest reliable path**를 선택한다.
검색 출처에 없는 모델 생성 URL은 진입점과 대안에서 제거한다.

출처가 확인되고 네트워크 정책을 통과한 진입 URL이 있으면 Google 메인 페이지나 사이트 홈을 거치지 않고
그 페이지를 먼저 연다. Navigator는 실행 그래프를 그대로 재생하지 않고 현재 DOM과 URL을 매 단계 다시 읽는다.
Verifier는 예상 신호와 현재 화면을 비교해 `destination`, `expected_stage`, `authentication`, `outside_route` 등으로
상태를 정리하며, 불일치하면 현재 화면에서 부분 재계획한다. 가격·재고·로그인·장바구니·완료 여부는 검색 결과를
증거로 확정하지 않고 실제 브라우저 화면에서 다시 검증한다. 사전 검색 도구가 현재 모델에서 지원되지 않거나
일시적으로 실패하면 작업을 버리지 않고 기존 브라우저 검색 경로로 전환한다. 작업 기록에는 리서치 경로와 검색
출처를 표시하며 출처 카드를 눌러 해당 공개 페이지를 열 수 있다.

비밀번호·OTP·CAPTCHA·카드 입력란이 보일 때 브라우저를 앞에 표시한다. 비밀번호·OTP·카드번호·유효기간·보안코드는
Android Keystore 키로 AES-GCM 암호화한 별도 기기 보관소에서 현재 HTTPS 문서의 검증된 입력란으로만 전달할 수 있다.
CAPTCHA는 저장하거나 자동 입력하지 않는다. 보안정보 값과 필드 목록은 모델 요청·대화·작업 기억·스크린샷에 포함하지 않는다.
현재 보안 입력 화면과 사용한 보안정보가 다시 노출된 화면은 채팅용 이미지 생성을 막는다. 로그인을 마치고
안전한 공개 결과 페이지로 이동하면 캡처를 다시 허용한다.
OTP는 저장 후 10분이 지나면 자동 채우기 후보로 사용하지 않는다. 사용자가 로그인을 마치고
보안 입력란이 사라진 안정된 화면이 두 번 확인되면 같은 목표와 근거를 유지한 채 자동으로 재개한다.
이름·수령인·주소·우편번호·전화·이메일은 채팅에서 질문하며 Android Keystore AES-GCM으로 암호화한
로컬 SQLite DB에 저장한다. 사이트와 사용 목적도 암호화해 기록하고 다음 입력에 재사용한다. 값 자체는
OpenAI 대화·작업 기록·로그에 넣지 않으며, 사이트가 값을 일반 DOM에 다시 표시해도 관찰 전에 마스킹한다.
설정에서 저장 항목과 최근 사용 목적을 보고 전부 삭제할 수 있다. API 키는 기존 SecureStore에 별도로 저장하며 페이지나 모델에 보내지 않는다.

결과는 이미지·출처와 함께 채팅에 표시한다. 검증된 현재 화면 캡처를 먼저 표시하고, 공개 페이지의 실제 상품 이미지를
기기에서 직접 불러오면 같은 자리를 원본으로 교체하고 카탈로그 카드에도 사용한다. 이미지 서버가 느리거나 실패해도 캡처는 남는다.
이미지 URL·쿠키·바이트는 모델에 보내지 않는다.
**작업 기록**도 같은 대화에 추가한다. 사이트의 단순 JavaScript 알림은
상태 안내로 표시하며 설정·사이트 confirm/prompt 등 필요한 대화상자는 유지한다.
공개 화면의 선택적 안내·홍보 대화상자는 실제 닫기 버튼임을 확인한 경우 AI가 추가 확인 없이
닫을 수 있다. 취소·약관 동의·주문 버튼을 닫기로 취급하지 않는다.

## Android AI 브라우저

Android 앱을 열면 네이티브 채팅 화면이 자동으로 열리고 WebView는 채팅 뒤에서 사용할 준비를 한다.
화면의 브라우저 켜기/끄기 버튼은 없으며 추론 라우터가 웹이 필요하다고 판단할 때만 페이지를 연다.
일반 대화에는 웹페이지를 읽거나 조작하지 않는다. 대화는 최대 24개 메시지·메시지당 12,000자·총 60,000자로 제한해 전달한다.
화면에는 최근 60개 메시지를 유지한다. Responses의 직접 대화 전달 방식은
[OpenAI 대화 상태 문서](https://developers.openai.com/api/docs/guides/conversation-state)를 따른다.

Chromium 기반 Android System WebView는 공개 HTTPS 주소만 열고 쿠키를 앱 안에 유지한다. 평소에는
채팅이 페이지를 덮고 있으며 보안 입력이나 결제 확인이 필요한 경우에만 페이지가 보인다. 뒤로·앞으로·
새로고침과 직접 주소 이동은 그 화면에서 사용할 수 있다. 로그인 쿠키는 휴대폰 Chrome과 별개다.

새 웹 작업에는 이전 대화에서 개인정보 검사를 통과한 최근 최대 6개 메시지(각 2,000자·총 8,000자)를
별도 참고 문맥으로 전달해 ‘그중에’ 같은 지시를 해석한다. 현재 작업 목표·조건표와 구분하며 이전 대화가
사이트 변경을 승인하거나 현재 지시를 덮어쓰지는 않는다.

AI의 추가 질문은 채팅과 알림에 남는다. 알림을 펼쳐 답변하면 앱을 열지 않아도 원래 작업에 반영한다.
확인된 공개 페이지의 결과·질문에는 현재 WebView의 이미지를 붙일 수 있다. 이미지는 캡처 시각·출처와
함께 표시하는 읽기 전용 기록이며 브라우저 열기 버튼으로 사용하지 않는다. OpenAI나 파일에 저장하지 않고
최대 6개·총 12MiB, 각 720×960 이내로 제한한다.

캡처는 같은 문서·주소·공개 관찰 상태를 다시 확인한 후에만 수행한다. 비밀번호·OTP·카드 입력란이 있는 현재 화면과
기기 보관소의 보안정보가 다시 감지된 화면은 이미지를 남기지 않는다. 계정 메뉴·일반 입력란·iframe이 있는 보통 페이지는
AI 입력과 분리된 사용자 전용 RAM 미리보기로 표시할 수 있다.
새 페이지·새 대화·작업 전환 후 도착한 이전 캡처 결과는 폐기한다. 개인정보 판단은 보수적인 보조 검사이며
웹사이트가 임의로 렌더링하는 모든 비공개 내용을 완벽하게 분류하는 기능은 아니다.

브라우저 상단 **설정**에서 API 키·모델·최대 실행 횟수(1~100회)·제한 시간(30~1800초)을
직접 입력할 수 있다. 대화 화면과 같은 SecureStore에 저장하며, 키 입력란을 비워 두면
기존 키를 유지한다. 저장된 키는 입력란에 표시하지 않는다. AI가 응답하거나 브라우저를 조작하는 동안
설정을 열고 닫거나 저장해도 현재 작업·요청 세대·대기 중인 페이지 동작은 취소하지 않는다.

채팅에 `러닝화를 찾아서 가격을 비교해 줘`처럼 목표를 입력하고 **보내기**를 누른다.
AI는 현재 공개 페이지의 본문·제목·폼·선택지와 정제된 HTML 구조를 읽고 다음 동작을 결정한다.
검색창 입력·검색 제출·링크 이동뿐 아니라 공개 일반 입력란, select, checkbox/radio,
영역 스크롤, 대상 상세 읽기, 대기, 뒤로 가기를 지원한다. 일반 입력·선택·체크·버튼·장바구니·예약 동작은
별도 승인 없이 실행하고 결과를 다시 관찰한다. 금액이 청구되는 마지막 결제 동작만 사용자에게 넘긴다.
비밀번호·OTP·카드 정보는 기기 보관소에서 저장 후 입력하거나 이번 한 번만 입력할 수 있고, CAPTCHA는 사용자가 페이지에 직접 입력한다. 불필요한 JavaScript alert는 상태 안내로
바꾼다. 같은 문서에서 열리는 dialog·modal·drawer는 클릭 직전/직후의 가시 상태와 클릭한 요소 ID를 대조해
클릭 성공으로 검증하고, 모달 안에서 관찰된 동작만 다음 후보로 제공한다. 파일 업로드·다운로드는 직접 처리해야 한다.
일반 새 창 링크는 현재 WebView에서 연다.

클릭은 먼저 현재 DOM 요소의 동일성·가시성·실제 hit target을 확인한 뒤 `element.click()`으로 실행한다.
그 결과가 같은 문서에서 2.5초 동안 확인되지 않은 경우에만 동일 URL·동일 요소에 한해 두 번째 전송 방식을 한 번 허용한다.
관찰된 공개 HTTPS `href`가 있으면 네이티브가 그 주소를 직접 열고, 링크가 없는 가역적 UI 컨트롤은 DOM preview에서
다시 확인한 중심 좌표에 Android 터치 이벤트를 보낸다. 이 터치는 사용자 조작으로 오인해 작업을 중지하지 않으며 결과는
다시 DOM으로 검증한다. 장바구니·구매·주문·예약·삭제·취소·계정·결제처럼 중복 실행 위험이 있는 컨트롤에는 이 우회를
사용하지 않는다. `target=_blank`나 `window.open()`은 별도 탭을 방치하지 않고 목적지가 공개 HTTPS인지 검사한 후 현재 탭으로 넘긴다.
사이트가 `intent://` 앱 링크를 요청하면 검증된 `browser_fallback_url` 또는 HTTP(S) intent 목적지만 HTTPS로 전환해 연다.
안전한 웹 폴백이 없으면 현재 페이지와 AI 작업을 종료하지 않고 해당 클릭만 실패로 기록하며, 모델은 같은 요소를 반복하지 않고
웹용 링크·다른 버튼·현재 페이지 안의 경로를 다시 선택한다. DNS 확인 실패나 차단된 자동 이동도 마지막으로 검증한 공개 페이지를 복구해 재계획한다.

상품 목록의 필터·정렬 UI가 `button`이나 ARIA role 없이 `div`/`span` 클릭 영역으로 구현된 경우에도
표시 문구, `cursor`, filter/sort/facet/refinement/accordion 힌트를 함께 확인해 제한된 버튼 후보로 만든다.
필터 drawer의 동작을 상품 카드보다 먼저 수집하고, 화면에 보이는 `label`이 숨은 checkbox/radio를 가리키면
라벨을 실제 선택 대상으로 사용해 체크 상태를 다시 읽는다. `Material` 같은 facet 문맥을 옵션과 함께 전달하며,
목표 조건과 일치하는 옵션만 체크한 뒤 `Apply`/`View results`를 실행하도록 플래너에 지시한다.
Algolia처럼 facet을 `a.refinement-link`로 만들고 기본 이동을 취소하는 페이지는 `active` 선택 상태를 별도로 읽는다.
필터 결과 버튼의 화면 문구가 `aria-label="Close"`와 충돌하면 `View N Products` 같은 실제 표시 문구를 우선하며,
그 버튼으로 drawer가 사라진 사실을 클릭 후조건으로 기록한다. 따라서 결과 버튼이 DOM에서 사라져도 실패로 판정해
필터를 다시 여는 반복이 발생하지 않는다.
판매처마다 DOM과 로그인 정책이 달라 모든 사이트의 구매 흐름을 보장하지 않는다.

웹 작업은 먼저 OpenAI `web_search` 출처에서 공개 HTTPS 진입 URL을 고르고, 출처와 일치하는 URL만 WebView에
직접 전달한다. 검색만으로 답할 수 있으면 WebView를 열지 않는다. 쿠팡처럼 AI WebView의 직접 요청에 401/403/429를
반환하는 판매처는 검색 실패로 오인하거나 재시도하지 않고 같은 WebView를 사용자에게 보여 준다. 사이트 확인·로그인·
사람 확인이 끝나면 **입력 완료·계속**으로 동일한 작업과 세션에서 다시 관찰한다.

**중지** 또는 페이지 탭은 진행 중 AI 요청을 취소한다.
스크롤·확대 제스처는 작업을 종료하지 않는다. 손가락이 닿아 있는 동안 이전 판단의 실행을
보류하고 스크롤 후 화면을 다시 읽어 이어간다. 아직 열리지 않은 이전 페이지 이동 요청도 무효화한다.
한 번의 실행은 기본 최대 20단계·300초이며 설정한 한도에 도달하면 멈춘다. 활성 작업 중 앱이 배경으로
이동하면 dataSync foreground service와 진행 알림을 사용해 WebView와 API 세션을 유지한다. 질문은 RemoteInput
답변 알림으로, 완료는 일반 알림으로 표시한다. Android가 프로세스를 강제 종료한 경우에는 메모리 작업을 복원하지 못한다.
**계속**은 같은 목표의 계획·근거를 유지하며
현재 페이지부터 다시 확인하고, **보내기**는 새 웹 작업을 시작한다. 실행 한도는 시작·재개마다 다시 적용된다.
작업 기억은 최근 8개 페이지 발췌·16개 출처 포함 사실·8개 계획·6개 실행 중 웹 검색 결과로 제한하며 브라우저가 열린 동안의
메모리에만 보관한다. 대화 화면을 종료하거나 새 웹 작업·새 대화를 시작하면 삭제한다. 로그인 쿠키는 앱의 WebView
저장소에 유지되며 휴대폰 Chrome의 로그인 세션과는 별개다. API 키를 페이지 JavaScript·Intent·APK에 넣지 않는다.

페이지와 네이티브 사이에 `addJavascriptInterface`/웹 메시지 브리지를 노출하지 않고,
네이티브의 `evaluateJavascript` 결과만 받는다. DOM 코드는 WebView의 페이지 실행 환경에서
동작하므로 Chromium fork의 isolated world와 같은 격리를 제공하지 않는다. 페이지 내용은
신뢰하지 않는 입력으로 다루며, 네이티브에서 동작·대상·페이지 변경과 승인 여부를 재검증한다.
계정 영역·입력값·숨김 요소를 관찰에서 제외하지만 공개 페이지의 상품명·가격·링크·선택지·
정제 HTML 구조·누적된 공개 근거 등은 목표와 함께 OpenAI로 전송된다. 입력 성공 여부는 필드의 원래
값을 보내지 않고 페이지 안에서 대조한 boolean으로 확인한다. 페이지 실행 코드에는 필요한 동작
필드만 전달하며 다른 페이지의 기억·목표·모델 설명은 전달하지 않는다. 쇼핑몰 실계정·실결제 검증은 별도로 필요하다.

초기 라우터는 strict Structured Outputs JSON Schema로 `chat|browser`만 반환한다. 리서치 플래너도 strict schema와
`tools:[{type:"web_search"}]`, `tool_choice:"required"`, `web_search_call.action.sources`를 사용한다. 브라우저 AI 요청은 `input`의 system 메시지에 JSON 출력 지시를 넣고, 정제한 페이지 정보와
목표는 별도 user 메시지로 보낸다. HTTP 400/404 응답은 알려진 오류 코드·필드만 분류하여
모델 접근 문제와 입력·응답 형식 오류를 구분한다. 서버 오류 원문은 화면이나 로그에 노출하지 않는다.

페이지 이동·SPA 주소 변경을 감지하면 이전 판단과 대상 ID를 무효화하고 새 화면에서 다시 관찰한다.
페이지 로드 이벤트뿐 아니라 DOM 변경·busy 상태를 확인하고, 조작 후에는 실제 화면/요소의 변화를
다시 읽어 `applied` 또는 `unknown`으로 다음 판단에 전달한다. `applied`는 업무 완료 인증이 아니다.
동일 행동·변화 없음·오류가 반복되면 바로 중지하지 않는다. 페이지 준비 지연, 이미 적용된 상태,
스크롤 경계, 같은 주소 재이동, 실제 무반응을 먼저 분류한다. 추론 모델은 이전 동작의 목적·결과와
현재 화면을 비교해 원인과 다음 검증 방법을 짧게 설명하고 다른 경로를 선택한다. 이 진단 복구도
두 차례 실패한 경우에만 중지하고 분석 결과를 기록에 남긴다.

**작업 기록**에서 계획, 출처, 결과의 인용 문구와 최근 동작을 확인한다. `finish`는 관찰한
페이지에 존재하는 근거를 요구하며, 종료 응답에 근거가 없으면 미검증 부분 결과로 표시한다. 인용 일치만으로 목표의 모든 조건을
충족했음을 증명할 수는 없다. 이미지·캔버스, iframe, shadow DOM, 실제 여러 탭 제어는 미지원이다.
연구와 적용/보류 판단은 [연구 검토 문서](../docs/browser-agent-research-2026-09-25.md)에 기록했다.

목표 원문에서 조건표를 추출하면 이후에는 조건·선호 여부·적용 범위를 고정한다.
최대 12개 조건과 12개 후보의 확인 상태·출처를 별도로 기억하며 최근 페이지 발췌가 지워져도 이 근거는
작업이 끝날 때까지 유지한다. 각 후보에 적용하는 조건과 결과 집합 전체에서 확인할 조건을 구분한다.
필수 항목이 미확인·불충족인 상태, 결과 개수 미달, 중복 후보는 그대로 완료 처리하지 않는다.
완료를 표시하기 직전에도 현재 페이지를 새로 읽으며, 모델 응답 중 화면 상태가 바뀌면 재판단한다.
이는 이전에 방문한 모든 출처의 현재 가격·재고를 다시 조회하는 기능은 아니다.
조건 원문과 숫자 누락을 검사하지만 최초 조건 분해·제품 동일성·인용의 의미는 여전히 모델 판단이다.
일부 확인이 불가능하면 부분 결과와 한계를 표시하고 인계한다. **작업 기록·결과**에서 조건표를 볼 수 있다.

0.5.1에서는 조건표와 검색 실행을 분리했다. `task`·`state`의 형식 오류나 조건표 갱신 실패가
정상 검색·탐색을 막거나 반복 실패 횟수를 늘리지 않는다. 기존 조건은 유지하며 원래 목표로 작업을
계속한다. 완료 제안의 조건·근거가 부족하면 재관찰을 반복하는 대신 **미검증 부분 결과**로 표시한다.
URL·개인정보·실행 대상 검사와 최종 결제의 사용자 인계는 그대로 적용한다.

`search(query,purpose?,requirementIds?)`는 사전 리서치에 쓸 수 있는 진입점이 없거나 실행 중 새로 발견된 미확인
조건을 조사할 때 쓰는 브라우저 내 보조 검색이다. 코드는 고정 Google 검색 URL을 생성한다. 검색 이동이 확인된
질의만 최근 12개 검색 기록에 남기며 실패한 이동은 재시도를 막지 않는다.
조건표가 아직 없거나 조건 ID가 맞지 않아도 검색은 진행한다. 목적·조건 ID는 선택적인 계획 정보다.
같은 질의를 반복하기보다 표현·출처를 바꾸도록 피드백한다. 가격·재고는 검색 순위만으로 확정하지 않도록
지시하지만 독립적인 범용 수학·최신성 판정기는 아니다.

`research(query,purpose?,requirementIds?)`는 상품 페이지·로그인 세션·현재 선택 상태를 그대로 둔 채 OpenAI
`web_search`로 실행 중 발견한 정보 공백만 조사한다. 문자와 숫자 사이즈의 대응처럼 현재 UI를 선택하는 데 필요한
브랜드·상품군·성별/핏·판매 지역별 정보를 우선 공식 사이즈 가이드에서 찾는다. 검색 도구가 반환한 출처 URL과
일치하는 결과만 최대 6개 작업 메모리에 보관하고 같은 페이지를 다시 관찰한다. 출처가 충돌하거나 정확한 대응을
확정하지 못하면 임의 선택하지 않고 사이트 가이드를 더 확인하거나 사용자에게 묻는다. 이 결과는 옵션 선택의
보조 근거이며 현재 재고·가격·실제 선택 반영을 증명하지 않으므로, 선택 후 DOM 상태는 별도로 검증한다.

페이지 요소의 `group`·`context`로 같은 이름의 버튼이 속한 상품·표 행·폼을 구별한다. 해당 문맥이 바뀌면
예전 요소의 실행을 거절한다. 내부 스크롤은 창의 이동과 별도로 확인한다.
실패 원인별 피드백을 유지하며 잘못된 AI 응답/대상/미완료 응답은 새 관찰과 함께 최대 2회 다시 판단한다.
이 요청도 설정한 단계·시간 한도에 포함된다. 키·권한·과금·네트워크 오류는 자동 재전송하지 않는다.
브라우저 동작 계획은 기존 JSON mode와 네이티브 allowlist 검증을 함께 사용하며, 초기 요청 라우터만 strict JSON Schema를 사용한다.
첨부된 22개 연구 제안의 추가 적용·보류 이유는 [후속 검토](../docs/browser-agent-research-followup-2026-09-26.md)에 기록했다.

검증은 `npm test -- --silent`, `npm run typecheck`,
`python3 scripts/test-browser-agent.py --json-jar <org.json.jar> --android-jar <android-36/android.jar>`,
Gradle `:vitlane-browser:testReleaseUnitTest`로 수행한다. 실제 Chromium DOM fixture는 Playwright가
설치된 환경에서 `node modules/vitlane-browser/scripts/test-browser-page-agent-chromium.mjs`로 실행한다.
별도 설치 위치를 사용하면 `VITLANE_PLAYWRIGHT_MODULE`에 해당 Playwright 모듈 경로를 지정한다.
이 fixture는 네트워크 응답을 메모리에서 제공하며, 실제 API·로그인 계정 성공률 검증은 아니다.

구현 위치: `modules/vitlane-browser/android`. DOM 원본 수정 후에는
`node modules/vitlane-browser/scripts/sync-browser-page-script.mjs`로 Java 소스를 갱신한다.
이 경로는 Windows에서 일반 Android APK로 빌드하며 WSL·Chromium 전체 컴파일이 필요 없다.

## 서버 없이 Android APK 빌드

```bash
npm ci
npx eas-cli build --platform android --profile chat-apk
```

로컬에서는 Node.js, Java 17, Android SDK 36, Build Tools 36.0.0,
NDK 27.1.12297006, CMake 3.22.1을 준비하고 `JAVA_HOME`, `ANDROID_HOME`을 설정한다.
Windows PowerShell에서 실행한다.

```powershell
npm.cmd ci
$env:EXPO_PUBLIC_VITLANE_DATA_MODE = "openai"
$env:EXPO_NO_DOTENV = "1"
$env:NODE_ENV = "production"
npx.cmd expo prebuild --platform android --no-install
cd android
.\gradlew.bat :vitlane-browser:testReleaseUnitTest :app:assembleRelease '-PreactNativeArchitectures=arm64-v8a,armeabi-v7a,x86_64' --max-workers=2 --no-parallel
```

결과는 `android/app/build/outputs/apk/release/app-release.apk`이다. JavaScript가
포함되어 Metro 없이 실행된다. 로컬 기본 서명은 설치 테스트용 debug 인증서이며,
스토어 배포에는 별도 release 서명이 필요하다. 위 명령은 ARM64·ARM32 기기와 x86_64 에뮬레이터용이다.
최소 Android 7.0(API 24)이 필요하며 WebView 제공 앱을 최신 상태로 유지하는 것이 좋다.

이 모드는 Vitlane 서버 없이 대화와 Android WebView 브라우저를 사용한다.
기존 commerce 앱은 `EXPO_PUBLIC_VITLANE_DATA_MODE=server`로 선택할 수 있고 fixture는
개발 중 UI 검수에서만 명시적으로 켤 수 있다. 아래 설명은 이 선택 기능에 해당한다.

## 로컬 UI 검수

```bash
cp .env.example .env.local
# .env.local의 EXPO_PUBLIC_VITLANE_DATA_MODE를 fixture로 변경
npm run web
```

fixture 모드는 개발 빌드에서만 시작된다. release 빌드는 `server` 외 모드를 거절한다.

## 실제 서버 연결

`.env.local`에는 공개 가능한 앱 설정만 둔다.

```dotenv
EXPO_PUBLIC_VITLANE_DATA_MODE=server
EXPO_PUBLIC_VITLANE_API_ORIGIN=https://api.example.com
EXPO_PUBLIC_VITLANE_EXECUTION_MODE=LIVE
```

Google 로그인은 시스템 브라우저, PKCE S256, `vitlane://auth/callback` 일회용
handoff를 사용한다. 교환된 opaque 세션은 iOS Keychain 또는 Android
Keystore-backed SecureStore에 저장한다. 검색·Shopify·OpenAI 키는 루트 Server
환경에만 넣고 모바일의 `EXPO_PUBLIC_*`, EAS public env, `app.json`에는 넣지 않는다.

로컬 Server에서만 개발 계정을 사용할 때는 양쪽을 모두 명시한다.

```dotenv
# server
ALLOW_DEV_AUTH=true

# mobile
EXPO_PUBLIC_VITLANE_ENABLE_DEV_LOGIN=true
EXPO_PUBLIC_VITLANE_DEV_PROFILE=empty-user
```

## Android APK cloud build

EAS의 preview 환경에 `EXPO_PUBLIC_VITLANE_API_ORIGIN`을 등록한 뒤 실행한다.

```bash
npx eas-cli build --platform android --profile server-apk
```

`server-apk`는 실제 서버와 LIVE catalog research를 사용한다. 공급자 secret과
Google OIDC secret은 APK에 포함되지 않는다. 이 profile은 일반 Expo API client를
빌드하며 Android Chromium host가 결합된 AI 브라우저 APK는 아니다.

## 후보 브라우저 상태

후보를 누르거나 background finding 이동을 승인하면 fixture와 Server Workspace가 같은
`BrowserRunSheet` 흐름을 사용한다. Web export는 merchant iframe, credential field와 판매처 결제
control을 열지 않고 로그인 handoff, 새 observation, checkout 진입 확인과 사용자 직접 결제 상태만
검수한다. `SERVER_BROWSER_PUBLIC` 후보도 Server의 비영속 익명 `/v1/discover` 결과이며 사용자 로그인
browser를 AI가 조작해 찾은 결과가 아니다.

Workspace의 `판매처에서 직접 검색` 영역은 쿠팡과 네이버 예약·쇼핑을 포함한 고정 destination을
승인 뒤 같은 browser sheet에서 연다. iOS에서는 실제 persistent WKWebView가 처음부터 사용자 제어로
열리고, 네이버 예약은 `map.naver.com` 검색 결과에서 사용자가 장소와 예약 항목을 고른다. 이 직접
검색은 Candidate를 생성하거나 서버 BrowserRun을 시작하지 않으며 AI가 검색·로그인·예약을 대신했다고
표시하지 않는다. Web은 review simulator이고 Android는 Chromium host build가 필요하다.

iOS Server sheet는 app-scoped persistent WKWebView 한 인스턴스를 실제 mount해 사용자가 로그인한
같은 session을 유지한다. Android source는 별도 Chromium fork의 regular profile/WebContents를
사용하는 별도 경로다. 위의 OpenAI 모드용 Android WebView 브라우저와는 분리되어 있다.
현재 commerce 경로의 native binary·Android RN host·Device Channel·기기
permit enrollment·판매처 준비 recipe는 실기기에서 검증되지 않았다. iOS에서 구현한 재개 확인도
새 page identity의 정제 observation을 한 번 확인할 뿐 지속 AI 제어가 아니다. 로그인·계정·장바구니·
결제정보와 최종 결제 버튼은 항상 사용자 직접 조작 범위다.
