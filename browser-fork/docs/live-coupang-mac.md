# macOS 쿠팡 실브라우저 검수

이 도구는 fixture나 Expo Web 화면이 아니라 macOS의 공식 Google Chrome에서 쿠팡 상품 페이지를
직접 검수하기 위한 로컬 single-item 경로다. 사용자가 exact 상품·옵션·수량·가격 상한을 승인하면
고정된 `builtin.coupang.purchase-preparation@1` recipe가 검증 단계 뒤 `바로구매`를 최대 한 번
활성화한다. 그 직후 CDP를 분리하고 주문서·로그인 화면을 사용자에게 넘긴다.

이 경로에는 결제, `주문하기`, `결제하기`, 주문 확정, OTP 또는 CAPTCHA action이 없다. 도구를
실행해도 실제 주문 완료를 뜻하지 않는다.

## 실행 조건

- macOS의 `/Applications/Google Chrome.app`
- Node.js 22 이상
- `browser-fork` 의존성 설치
- 사용자가 직접 로그인할 쿠팡 계정

개인 Chrome profile은 읽거나 복사하지 않는다. launcher는 운영체제 임시 디렉터리에 실행별 Vitlane
전용 profile을 만들고 `--remote-debugging-address=127.0.0.1 --remote-debugging-port=0`으로 공식
Chrome을 연다. 이 profile의 쿠키는 Chrome 안에만 남으며 control server, page agent, 로그에 출력되지
않는다. 다음 실행에서는 새 profile을 만들므로 다시 로그인해야 한다.

```bash
cd browser-fork
npm run try:coupang
```

launcher가 연 로컬 control 화면과 쿠팡 탭에서 다음 순서로 진행한다.

1. 쿠팡 탭에서 직접 로그인한다. 비밀번호, passkey, OTP와 CAPTCHA는 Vitlane이 읽거나 입력하지 않는다.
2. 한 상품의 실제 상세 페이지를 같은 탭에서 연다. URL에는 `productId`, 정확히 하나의 `itemId`,
   정확히 하나의 `vendorItemId`가 있어야 한다.
3. control 화면에서 상품 탭을 확인한다. 로그인·account·cart·checkout 탭이면 DOM을 관찰하지 않고
   거절한다. 일치하는 상품 탭이 둘 이상이어도 거절한다.
4. 수량, 개당 가격 상한, 전체 가격 상한을 입력한다. 옵션 상품은 화면에 보이는 정확한 이름을
   `그룹=값` 형식으로 한 줄씩 입력한다. 옵션이 없다면 비워 둔다.
5. control 화면에 표시된 세 상품 ID, 옵션, 수량과 두 가격 상한을 확인하고 명시적인 승인 버튼을
   누른다. 승인은 5분 뒤 만료된다.
6. 옵션 선택, 옵션·가격 확인, 수량 설정이 모두 exact match일 때만 `바로구매`를 한 번 활성화한다.
   주문서로 이동하면 자동화가 즉시 끝난다. 이후 내용은 Chrome에서 직접 확인하고 진행한다.

## 실패 방식

실사이트 DOM이 고정 recipe와 조금이라도 다르면 generic selector나 좌표 click으로 대체하지 않는다.
다음 조건은 모두 중단 사유다.

- 상품 탭 URL이 승인 전후 또는 step 사이에 달라짐
- identity query가 빠졌거나 중복됨
- 옵션 group/value가 없거나 중복됨
- 가격을 하나로 판정할 수 없거나 상한을 넘음
- 수량 control 또는 `바로구매` control이 없거나 둘 이상임
- 승인 digest·만료·command 순서 또는 permit 검증 실패
- 로그인, account, cart, checkout 또는 결제 surface로 바뀜

`바로구매` click 뒤 navigation 때문에 결과를 받지 못하면 `outcome_unknown`으로 끝내고 자동 재시도하지
않는다. 현재 보이는 Chrome 화면을 직접 확인해야 한다. control 화면의 재실행으로 같은 승인을 반복하지
않는다.

## 보안 경계

- control server는 `127.0.0.1`의 임의 port에만 bind한다.
- run마다 새 control token을 만들고 state-changing request는 custom header로 token을 제출한다.
- CORS를 허용하지 않고 foreign `Origin`, 잘못된 `Host`와 cross-site request를 거절한다.
- CDP는 사용자가 상품 탭을 준비하고 로컬 control 화면에서 확인을 요청한 뒤에만 연결한다. control,
  로그인, account, cart와 checkout 탭의 DOM은 읽지 않는다.
- page agent는 isolated world에서 실행하고 input value, cookie, storage, network body와 screenshot을
  수집하지 않는다.
- 로그인 header는 공개 상품 관찰에서 제외한다. 로그인/account/cart/checkout 전체 화면은 관찰하지
  않고 사용자 handoff로 닫힌다.
- `바로구매` 시도 뒤에는 URL이나 DOM을 다시 읽지 않고 CDP session을 분리한다.
- 전용 Chrome 창을 닫으면 loopback CDP endpoint도 종료된다. 실행별 임시 profile은 저장소나
  evidence로 복사하지 않으며 Chrome을 닫은 뒤 표시된 경로를 삭제할 수 있다.

## 자동 검증과 실사이트 한계

```bash
npm test
PLAYWRIGHT_CHANNEL=chrome npm run test:browser
```

unit test는 URL identity, option 입력, 가격 상한과 approval digest를 검증한다. Chrome synthetic test는
실제 isolated world, protocol validation, monotonic permit issuer를 거쳐 옵션·수량 뒤 `바로구매` 한 번만
click하고 `결제하기`는 누르지 않는지 확인한다. 중복 `바로구매`와 step 사이 URL 변경도 fail closed로
검증한다.

쿠팡은 fresh headless Chrome 요청에 `403 Access Denied`를 반환했다. 따라서 이 도구는 headless 우회를
시도하지 않고 공식 headed Chrome에서 사람이 먼저 로그인하고 상품을 연다. 실사이트 DOM은 fixture와
다를 수 있으며 첫 승인 계정 smoke 전에는 성공을 주장하지 않는다. 첫 smoke도 주문서 진입까지만
확인하고 최종 주문·결제는 실행하지 않는다.
