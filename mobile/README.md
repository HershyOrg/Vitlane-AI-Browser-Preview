# Vitlane mobile

Expo SDK 57의 iOS·Android 공통 앱이다. 기본 런타임은 Vitlane Server이며 fixture는
개발 중 UI 검수에서만 명시적으로 켤 수 있다.

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
사용하며 System WebView로 대체하지 않는다. 현재 native binary·Android RN host·Device Channel·기기
permit enrollment·판매처 준비 recipe는 실기기에서 검증되지 않았다. iOS에서 구현한 재개 확인도
새 page identity의 정제 observation을 한 번 확인할 뿐 지속 AI 제어가 아니다. 로그인·계정·장바구니·
결제정보와 최종 결제 버튼은 항상 사용자 직접 조작 범위다.
