# Vitlane AI Browser Preview

Vitlane은 사용자의 쇼핑 목적을 받아 후보를 조사하고, 선택형 질문·예산 조정·후속
수정을 하나의 작업 흐름으로 연결하는 iOS·Android 공통 앱입니다. 이 공개 저장소는
모바일 UI, Go API, 판매처 조사 어댑터와 제한된 브라우저 실행 실험 코드를 검토할 수
있는 소스 스냅샷입니다.

## 포함된 구성

- `mobile`: Expo/React Native 기반 iOS·Android·Web 공통 UI
- `server`: 작업, 대화, 후보 조사와 BrowserRun 기록을 제공하는 Go API
- `browser-fork`: Android Chromium patch, iOS WebKit adapter, 로컬 AI bridge와
  쿠팡 단일 상품 준비 recipe
- `web`: 기존 웹 클라이언트와 디자인 시스템
- `shared`: OpenAPI, 이벤트와 공통 계약

상품 조사 공급자와 모델을 설정하면 목적 입력부터 실제 서버 조사, 후보 갱신, 예산
변경과 후속 요청까지 실행할 수 있습니다. API 키는 서버의 `.env`에만 두며 앱의
`EXPO_PUBLIC_*` 값이나 Git 저장소에는 넣지 않습니다.

## 현재 브라우저 실행 범위

Web 화면의 브라우저 영역은 UI 검수용 시뮬레이터입니다. 일반 웹페이지는 브라우저의
동일 출처 정책 때문에 로그인된 쿠팡·네이버 같은 다른 사이트의 탭이나 iframe을 직접
읽고 클릭할 수 없습니다.

Android 앱에는 다음 단계의 네이티브 AI 브라우저가 들어 있습니다.

- 일반 대화와 웹 작업을 나누고, 검색 공간을 결정하는 필수 조건 한 가지만 먼저 묻는 `gpt-6-sol` 추론 라우터
- 공개 페이지 관찰, 페이지를 유지하는 실행 중 웹 검색, 이동·입력·선택·스크롤, 같은 페이지 modal/drawer와 실행 결과 재검증
- 로그인·OTP·카드 입력과 마지막 결제만 사용자에게 넘기는 채팅형 작업 흐름
- AI에서 격리된 기기 보안정보 보관소, 암호화된 연락·배송 정보와 사용 목적 기록, 카탈로그 결과, 백그라운드 알림 답변

`browser-fork`에는 macOS Chrome harness, Chromium patch, iOS WKWebView adapter와 Go
BrowserRun 실험 코드가 별도로 남아 있습니다. Android 0.8.25의 동작 범위와 검수 명령은
[`mobile/README.md`](mobile/README.md)를 확인하세요. 보안정보는 기기 보관소 또는 열린 사이트에서 입력하며 최종 결제는 사용자가 직접 수행합니다.

## 로컬 실행

Docker가 실행 중인 환경에서 기본 서버와 웹 앱을 시작합니다.

```bash
cp .env.example .env
docker compose up --build
```

브라우저에서 `http://127.0.0.1:8080`을 엽니다. Docker와 Foundry가 설치된 환경에서
로컬 검수 환경 전체를 준비하려면 다음을 사용합니다.

```bash
./scripts/local-review.sh
```

모바일 UI만 검수하려면:

```bash
cd mobile
npm install
cp .env.example .env.local
EXPO_PUBLIC_VITLANE_DATA_MODE=fixture npm run web
```

실제 서버 모드는 `mobile/README.md`의 설정을 따릅니다.

## 서버 전용 환경변수

필요한 공급자만 선택해 루트 `.env`에 설정합니다. `.env.example`에는 실제 값을
입력하지 마세요.

```dotenv
MANAGED_OPENAI_API_SECRET=
AMAZON_PRODUCT_SEARCH_API=
SERP_API_KEY=
OPEN_WEB_NINJA_API_KEY=
APIFY_API_TOKEN=
SHOPIFY_DEV_CLIENT_ID=
SHOPIFY_DEV_CLIENT_SECRET=
```

## 검증

```bash
cd mobile && npm test && npm run typecheck
cd ../browser-fork && npm test
cd ../server && go test ./...
```

이 저장소는 공개 검토용 단일 스냅샷입니다. 운영 배포·복구 문서, production 증거와
비공개 Git 이력은 포함하지 않습니다. 공개 열람 및 재사용 조건은 [`LICENSE`](LICENSE)를
따릅니다.
