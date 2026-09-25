# 테스트 구조

- `conformance/`: HTTP, MCP, Executor 공유 규격 적합성 검증
- `integration/`: PostgreSQL과 외부 staging adapter 검증
- `e2e/`: Phase별 닫힌 사용자·Agent pipeline 검증

테스트는 각 공개 기능의 완료 시나리오를 기준으로 추가한다. Web3 스마트컨트랙트 테스트와 혼동하지 않도록 `contract`라는 테스트 디렉터리 이름을 사용하지 않는다.
