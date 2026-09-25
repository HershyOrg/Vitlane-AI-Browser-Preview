package postgres

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// process single-writer의 아키텍처 계약이다(ADR-0056 §1): agency_order_processes
// 를 쓰는 SQL 리터럴은 ordering/process/infra 밖에 존재할 수 없다. 종전에는
// 직접 UPDATE가 7곳이었고, 이 guard가 재발을 막는다. (읽기 SELECT·마이그레이션·
// 테스트 fixture는 대상이 아니다.)
func TestProcessTableHasSingleWriter(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	internalRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../.."))
	selfDir := filepath.Clean(filepath.Dir(filename))

	// account의 PII 삭제 캐스케이드(DELETE FROM — 사용자 소거)는 데이터
	// 수명주기이지 워크플로 쓰기가 아니다 — stage를 만드는 UPDATE/INSERT만
	// single-writer 대상이다.
	writePattern := regexp.MustCompile(`(?i)(UPDATE\s+agency_order_processes|INSERT\s+INTO\s+agency_order_processes)`)
	var offenders []string
	err := filepath.Walk(internalRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasPrefix(filepath.Clean(path), selfDir+string(os.PathSeparator)) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if writePattern.Match(body) {
			relative, _ := filepath.Rel(internalRoot, path)
			offenders = append(offenders, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) != 0 {
		t.Fatalf("agency_order_processes writes outside ordering/process/infra: %v", offenders)
	}
}

// 타 제품 소유 outbox의 교차 쓰기 재발 방지 — GIWA repo가 agency_order_outbox를
// 직접 UPDATE하던 형상(컷오버 B에서 테이블째 흡수 예정)의 확산을 막는다.
func TestNoNewCrossContextProcessEventWriters(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	internalRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../.."))
	selfDir := filepath.Clean(filepath.Dir(filename))
	// applied_* 컬럼은 Manager 소유다(ADR-0056 §2) — 이벤트 소비 마킹이 process
	// infra 밖에 나타나면 소유가 깨진 것이다.
	appliedPattern := regexp.MustCompile(`(?i)UPDATE\s+order_process_events`)
	var offenders []string
	err := filepath.Walk(internalRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasPrefix(filepath.Clean(path), selfDir+string(os.PathSeparator)) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if appliedPattern.Match(body) {
			relative, _ := filepath.Rel(internalRoot, path)
			offenders = append(offenders, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) != 0 {
		t.Fatalf("order_process_events consume-marking outside ordering/process/infra: %v", offenders)
	}
}
