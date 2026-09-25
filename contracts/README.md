# Vitlane Contracts

이 디렉터리는 GIWA TEST settlement의 Solidity source of truth다. HTTP API와 Go domain schema를
두지 않는다. Solidity `0.8.30`은 `foundry.toml`, Foundry `v1.7.1`은
`.foundry-version`에 고정한다.

## 구조

```text
contracts/
├── foundry.toml
├── remappings.txt
├── src/
│   ├── token/VitlaneTestUSD.sol
│   ├── faucet/VitlaneFaucet.sol
│   ├── settlement/VitlaneSettlement.sol
│   ├── interfaces/
│   └── libraries/SettlementTypes.sol
├── script/
│   ├── DeployGiwaSepolia.s.sol
│   ├── ConfigureGiwaSepolia.s.sol
│   ├── VerifyDeployment.s.sol
│   ├── ConfigureUnifiedTestPrincipals.s.sol
│   ├── VerifyUnifiedTestPrincipals.s.sol
│   ├── RotateTestOwnership.s.sol
│   ├── VerifyOwnedTestConfiguration.s.sol
│   ├── export-artifacts.sh
│   └── source-tree-hash.sh
├── test/
│   ├── unit/
│   ├── fuzz/
│   ├── invariant/
│   └── integration/
└── deployments/91342/
    └── schema.json
```

## Contract 책임

### VitlaneTestUSD

- ERC-20 name `Vitlane Test USD`, symbol `tVITUSD`, decimals `6`
- Faucet-only mint
- token 자체 pause 없음. 신규 mint와 pay는 Faucet/Settlement에서 제어
- 경제적 가치, USD backing과 redemption을 주장하지 않음

### VitlaneFaucet

- caller 대상 fixed claim
- address cooldown, per-claim amount와 global daily cap
- Faucet admin/pauser
- Settlement와 독립된 장애·권한 경계

### VitlaneSettlement

- immutable token
- EIP-712 exact pass-through+fee payment authorization
- active merchant registry version/recipient와 current fee config 강제
- exact transferFrom 뒤 fee는 즉시 TEST fee recipient로 이체, pass-through만 escrow
- internal merchant payout registry
- complete는 누적 환불 뒤 잔여 pass-through만 principal recipient로 방출
- `refundPartial`은 ESCROWED/COMPLETED에서 pass-through·fee 누적 cap과 refundKey
  replay 방지를 강제
- COMPLETED pass-through와 모든 fee refund는 각 TEST 수취 계좌 allowance-pull
- refundAfter 뒤 permissionless trigger는 escrow 잔여 pass-through만 payer에게 반환
- pause 중 refund 유지
- active escrow sweep과 proxy upgrade 없음

이 Settlement 구현은 경제적 가치가 없는 test token 전용이다. fee 즉시 수취와 수취 계좌
allowance-pull 환불은 가치 자산 custody·환불 지급 능력을 보장하지 않는다. canonical
asset 또는 다른 가치 토큰 Live는 ADR-0051의 principal+fee 우선 보관, 단계 방출,
fee earning, reserve/treasury 고객 환불 선지급과 독립 merchant recovery를 구현한 새
contract/control account와 manifest를 요구한다.

## 생성 산출물

`forge build` artifact에서 다음을 결정적으로 생성한다.

```text
shared/onchain/abi/v1/*.json
server/internal/ordering/payment/giwa/infra/evm/zz_generated_abi.go
web/src/shared/onchain/abi.ts
shared/onchain/deployments/91342.json
```

현재 구현은 `script/export-artifacts.sh`로 versioned ABI와 Server/Web consumer ABI를
함께 생성한다. Server는 이 ABI를 startup에서 manifest/runtime code와 함께 검증한다.
reviewed active deployment가 생기기 전에는 consumer deployment 주소 파일을 만들지
않는다. 공개 preview에는 실제 배포 주소 파일이 포함되지 않는다.

생성 파일에는 Foundry의 환경 의존적인 top-level artifact id와 raw metadata 문자열을
제외하고, ABI·bytecode·method identifiers·정규화한 compiler metadata로 계산한
canonical artifact hash를 기록한다. CI는 이 값과 생성 파일 drift를 검사한다. 손으로
ABI, Go binding 또는 Web address를 수정하지 않는다.

## Deployment manifest

manifest는 별도로 관리하는 deployment evidence다. 이 공개 preview는 schema만 포함하고
실제 주소, 역할 지갑, transaction hash와 block number는 포함하지 않는다.

- `phase5-vNNN.json` 같은 version file은 추가만 하고 수정하지 않는다.
- `index.json`은 모든 version의 상태와 application이 소비할 `activeVersion`을 가진다.
- active version에서 생성한 consumer manifest만
  `shared/onchain/deployments/91342.json`에 둔다.

필수 필드:

- schema version과 environment label
- chain ID
- contract 이름과 주소
- deployment tx hash와 block number
- runtime bytecode hash
- compiler/settings와 constructor args
- ABI version
- admin, minter, signer, finalizer, pauser와 수신처 public address
- source commit, provenance와 재현 가능한 Solidity source tree hash
- source verification 상태
- `ACTIVE | PAUSED | REJECTED | RETIRED`

금지 필드:

- private key, mnemonic, signer credential
- private RPC URL 또는 API token
- EIP-712 signature 원문
- 사용자 개인정보와 Dojang 원문

## 기본 검증

구현 뒤 표준 명령은 다음 의미를 가져야 한다.

```bash
forge fmt --check
forge clean
forge build
forge test
forge test --match-path 'test/fuzz/*'
forge test --match-path 'test/invariant/*'
```

배포는 test 통과만으로 승인되지
않고 운영자가 별도로 검토한 역할·주소·source verification 절차와
smoke gate를 모두 통과해야 한다.

## 변경 정책

- deployed bytecode를 git rollback으로 되돌릴 수 있다고 표현하지 않는다.
- 수정은 기존 contract pause/refund 보장 뒤 새 immutable 주소로 배포한다.
- 기존 manifest를 덮어쓰지 않고 새 version을 추가한다.
- admin handoff 뒤 TestPhase Principal을 통일하고 Generic Web을 추가할 때는 최초
  배포 script를 재실행하지 않고
  `ConfigureUnifiedTestPrincipals.s.sol`과 `VerifyUnifiedTestPrincipals.s.sol`을
  사용한다. 기존 세 entry는 version 2, Generic Web은 version 1이어야 한다.
- 소유자 EOA로 Token·Faucet·Settlement admin/pauser를 넘기고 Fee 수령처를 바꿀
  때는 `RotateTestOwnership.s.sol`을 사용한다. authorizer/finalizer/refunder는
  수령처나 admin이 아닌 자동화 전용 signer이므로 이 script가 변경하지 않는다.
  `VerifyOwnedTestConfiguration.s.sol`이 역할·수령처·merchant registry를 함께
  readback한다.
- canonical asset 전환은 Faucet 없이 공식 token 주소를 사용하되 새
  Settlement/control-account 구조를 검토해야 한다.
- 실제 USDC 주소와 decimals는 배포 당일 공식 source로 재검증한다.
