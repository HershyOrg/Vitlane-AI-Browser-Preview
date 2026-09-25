# Onchain 공유 산출물

`contracts/`의 reviewed Foundry artifact에서 생성한 consumer용 ABI를 둔다.

```text
shared/onchain/
└── abi/v1/
```

- Solidity source of truth는 `contracts/`다.
- Server와 Web은 versioned ABI 산출물을 소비한다.
- ABI를 손으로 수정하지 않는다.
- private RPC, key, signature, 사용자 데이터는 포함하지 않는다.
- generator drift가 있으면 CI가 실패해야 한다.

Phase 5 구현에서는 `contracts/script/export-artifacts.sh`가 Foundry build 결과에서
공유 JSON ABI, Server의 `zz_generated_abi.go`와 Web의 `abi.ts`를 함께 생성한다.
deployment 주소 파일은 reviewed `ACTIVE` manifest가 생긴 뒤에만 생성하며, 실제
배포 매니페스트와 주소는 이 공개 preview에 포함하지 않는다.
