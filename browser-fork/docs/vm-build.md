# Chromium Android Linux 빌드 호스트

Chromium의 Android build는 Linux x86-64 host에서 실행한다. 현재 Apple Silicon Mac은 공통 UI,
protocol, iOS와 patch 검증에 사용하고 Android Chromium compile host로 사용하지 않는다.

## 현재 상태

- 고정 revision과 재현 가능한 patch source는 준비됐다.
- 로컬 Chromium checkout은 patch의 고정 revision과 다르고 Android용 `.gclient`/dependency가 없다.
- Android native Java/JNI/C++ compile과 APK 생성은 아직 실행되지 않았다.
- 이전 Docker 경로는 사용자가 중지했으며 이 작업에서 재시작하지 않았다.

## 권장 host

- Ubuntu x86-64
- RAM 16GB 이상, 가능하면 32GB
- 여유 SSD 150GB 이상
- Git 2.43.1 이상, Python 3와 system package 설치 권한
- 장기 source/cache를 유지할 수 있는 self-hosted runner 또는 전용 VM

표준 GitHub-hosted runner는 디스크와 build 시간 때문에 Chromium 전체 compile에 적합하지 않다.
루트 workflow의 `self-hosted, linux, x64, chromium` label은 위 자원을 갖춘 전용 runner를 뜻한다.

## 실행

Vitlane monorepo checkout에서:

```bash
cd browser-fork
python3 scripts/chromium_patch.py --check
bash scripts/build-android.sh
```

큰 source와 depot_tools를 별도 volume에 두려면 다음 변수를 사용한다. 기존 `LANE_*` 이름은 이전
proof와의 호환 기간에만 fallback으로 지원한다.

```bash
export VITLANE_CHROMIUM_DIR=/mnt/chromium
export VITLANE_DEPOT_TOOLS_DIR=/mnt/depot_tools
export VITLANE_BUILD_JOBS=4
export VITLANE_SYNC_JOBS=8
bash scripts/build-android.sh
```

스크립트는 dirty checkout을 reset하지 않는다. 고정 revision의 clean checkout이 아니거나 예상 patch
외 변경이 있으면 중단한다. package 설치와 `gclient runhooks` 뒤 Java integration을 먼저 compile하고
`chrome_public_apk`를 만든다.

## 산출물과 다음 검증

성공 시 `browser-fork/out/VitlaneBrowser-arm64.apk`를 만든다. APK 생성만으로 완료하지 않고 다음을
별도로 기록한다.

1. exact upstream revision, patch hash, GN args와 compiler 결과
2. 테스트 기기 model/Android version과 install 결과
3. native origin 표시, foreground/background, navigation invalidation
4. stop과 command 도착 경합, takeover와 fresh observation 재개
5. password·OTP·payment page handoff와 sensitive data 미전송
6. renderer 종료와 outcome unknown 뒤 자동 재실행 없음

release package ID, 브랜드, 서명, third-party notice와 update pipeline은 `chrome_public_apk` 검증 뒤의
별도 배포 범위다.
