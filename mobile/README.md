# Vitlane mobile

Expo SDK 57의 iOS·Android 공통 앱이다. 기본 화면은 별도 Vitlane 서버 없이 OpenAI
Responses API에 직접 연결하는 개인용 대화 앱이다. Android에는 **AI 브라우저**도 포함한다.
설치 후 **OpenAI 연결**에서 본인의
API 키를 입력하면 된다. 키와 모델 설정은 기기의 SecureStore에 저장하며 APK나
`EXPO_PUBLIC_*`에 API 키를 포함하지 않는다. 기본 모델은 `gpt-5-mini`이며 설정에서
변경할 수 있다. 대화는 메모리에만 유지하고 API 요청에는 `store: false`를 사용한다.
앱 재시작 또는 새 대화 시작 시 기록이 사라진다.

연동 명세: [OpenAI 텍스트 생성 문서](https://developers.openai.com/api/docs/guides/text).
각 요청은 현재 대화를 전달하며 최대 출력 4,096토큰, 120초 제한을 사용한다. 네트워크
실패나 취소 시 입력 내용을 복원하며 자동으로 재과금될 수 있는 재전송은 하지 않는다.

## Android AI 브라우저

대화 화면의 **AI 브라우저 열기**를 누르면 기기의 Chromium 기반 Android System WebView를
사용하는 별도 브라우저 화면이 열린다. 주소/검색어 입력, 뒤로·앞으로·새로고침, 쿠팡 바로가기를
지원한다. API 키가 없어도 직접 웹사이트를 열 수 있다. AI 기능에는 설정에서 입력한 키를 사용한다.

브라우저 상단 **설정**에서 API 키·모델·최대 실행 횟수(1~20회)·제한 시간(30~300초)을
직접 입력할 수 있다. 대화 화면과 같은 SecureStore에 저장하며, 키 입력란을 비워 두면
기존 키를 유지한다. 저장된 키는 입력란에 표시하지 않는다.

브라우저 아래에 `러닝화를 찾아서 가격을 비교해 줘`처럼 목표를 입력하고 **AI 실행**을 누른다.
AI는 현재 공개 페이지를 읽고 검색창 입력·검색 제출·링크 이동·스크롤과 요소 선택을 수행한다.
검색 외 버튼과 구매·장바구니 관련 변경은 네이티브 확인창에서 승인한 뒤 실행한다.
비밀번호·인증·개인 주문·장바구니·결제 화면에서는 AI 관찰과 전송을 멈추며 사용자가 직접 조작한다.
최종 주문·결제 버튼은 AI가 실행하지 않는다. 지원되지 않는 입력란·팝업·다운로드도 직접 처리해야 한다.
판매처마다 DOM과 로그인 정책이 달라 모든 사이트의 구매 흐름을 보장하지 않는다.

**중지**, **직접 조작**, 페이지 터치 또는 앱 전환은 진행 중 AI 요청을 취소한다.
한 번의 실행은 기본 최대 20단계·300초이며 설정한 한도에 도달하면 멈춘다. 앱이 배경으로
이동하면 브라우저가 가진 API 키 복사본을 지운다. 복귀 후 상단 **설정**에서 키 입력란을
비운 채 **저장**하면 기존 키를 다시 불러와 AI를 사용할 수 있다. 로그인 쿠키는 앱의 WebView
저장소에 유지되며 휴대폰 Chrome의 로그인 세션과는 별개다. API 키를 페이지 JavaScript·Intent·APK에 넣지 않는다.

페이지와 네이티브 사이에 `addJavascriptInterface`/웹 메시지 브리지를 노출하지 않고,
네이티브의 `evaluateJavascript` 결과만 받는다. DOM 코드는 WebView의 페이지 실행 환경에서
동작하므로 Chromium fork의 isolated world와 같은 격리를 제공하지 않는다. 페이지 내용은
신뢰하지 않는 입력으로 다루며, 네이티브에서 동작·대상·페이지 변경과 승인 여부를 재검증한다.
로그인 관련 영역·입력값·숨김 요소를 관찰에서 제외하지만 공개 페이지의 상품명·가격·링크 등은
목표와 함께 OpenAI로 전송된다. 쇼핑몰 실계정·실결제 검증은 별도로 필요하다.

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
