# 완료 게이트의 결정적 비교

[실행 결과 JSON](browser-criteria-gate-comparison-2026-09-26.json)은 같은 관찰·페이지 인용문을 두 규칙에 전달한 7개 fixture 결과다. 기준은 0.4.0의 인용 존재 검사 규칙이며, 전체 과거 APK를 실행한 A/B 시험은 아니다. 비교 대상은 그 검사에 0.5.0의 `BrowserTaskContract.canFinish`를 추가한 경우다. 실제 API·웹사이트·사용자 계정을 사용하지 않았다.

| 사례 | 인용 존재만 검사 | 조건 게이트 추가 |
| --- | --- | --- |
| 세 개 요청에 후보 한 개 | 허용 | 거절 |
| 후보 하나의 필수조건 근거 누락 | 허용 | 거절 |
| 같은 후보 ID로 수량 부풀림 | 허용 | 거절 |
| 세 후보·필수조건·출처가 구조상 충족 | 허용 | 허용 |
| A/B 비교에서 B 누락 | 허용 | 거절 |
| A/B 각 근거를 서로 다른 후보에 저장 | 허용 | 허용 |
| 예산 50만원에 실제 70만원이라는 인용문을 모델이 `supported`로 잘못 분류 | 허용 | **허용** |

새 게이트는 준비한 네 종류의 구조적 결손을 거절한다. 마지막 반례는 실제 문구를 인용해도 의미·숫자 비교를 잘못 판단할 수 있음을 보여준다. 이 결과로 전체 작업 성공률, 잘못된 완료 선언 비율의 감소량, 검색 정확도나 사용자 비용을 추정할 수 없다. fixture 입력과 기대 동작을 정한 개발자 시험이며 모델 판단을 평가하지 않는다.

재현용 [Java fixture](BrowserCriteriaGateComparison.java), [실행 스크립트](run-criteria-gate-comparison.py)를 사용한다. 저장 결과에는 컴파일한 소스의 SHA-256을 포함한다. 저장된 결과 이후 코드가 바뀌면 같은 명령을 다시 실행해야 한다.

저장소 루트에서 Java와 `org.json` JAR 경로를 지정한다. 경로는 각 개발 환경에 맞게 바꾼다.

```sh
python3 docs/evaluation/run-criteria-gate-comparison.py \
  --javac /opt/homebrew/opt/openjdk@21/bin/javac \
  --java /opt/homebrew/opt/openjdk@21/bin/java \
  --json-jar /Users/hongraecho/.gradle/caches/modules-2/files-2.1/org.json/json/20180813/8566b2b0391d9d4479ea225645c6ed47ef17fe41/json-20180813.jar \
  --output docs/evaluation/browser-criteria-gate-comparison-2026-09-26.json
```

실행 스크립트는 임시 디렉터리에 소스를 복사해 컴파일한 뒤 삭제한다. 일반 앱 테스트 runner나 Android 빌드에는 이 비교 실험을 추가하지 않는다.
