package postgres

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func internalRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../.."))
}

func productionGoFiles(t *testing.T, root string, visit func(string, []byte)) {
	t.Helper()
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		visit(path, body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// payment instruction 상태 쓰기는 AgencyOrder infra에만 존재해야 한다. Payment와
// GIWA는 consumer-side app port를 호출하고 ambient transaction을 공유한다.
func TestPaymentInstructionHasSingleWriter(t *testing.T) {
	root := internalRoot(t)
	owner := filepath.Clean(filepath.Dir(filepath.Dir(filepath.Dir(runtimeFile(t)))))
	writePattern := regexp.MustCompile(`(?i)UPDATE\s+agency_order_payment_instructions`)
	var offenders []string
	productionGoFiles(t, root, func(path string, body []byte) {
		if strings.HasPrefix(filepath.Clean(path), owner+string(os.PathSeparator)) {
			return
		}
		if writePattern.Match(body) {
			relative, _ := filepath.Rel(root, path)
			offenders = append(offenders, relative)
		}
	})
	if len(offenders) != 0 {
		t.Fatalf("payment instruction writes outside AgencyOrder: %v", offenders)
	}
}

// agency_order_execution_units는 immutable archive다. GIWA의 command planner와
// broadcast 경로가 이 archive를 운영 projection처럼 다시 읽지 못하게 한다.
func TestGIWADoesNotReadExecutionUnitArchive(t *testing.T) {
	root := internalRoot(t)
	giwaRoot := filepath.Join(root, "ordering", "payment", "giwa")
	legacyCommand := regexp.MustCompile(`\bCommandRefund\b`)
	var offenders []string
	productionGoFiles(t, giwaRoot, func(path string, body []byte) {
		if strings.Contains(string(body), "agency_order_execution_units") ||
			legacyCommand.Match(body) {
			relative, _ := filepath.Rel(root, path)
			offenders = append(offenders, relative)
		}
	})
	if len(offenders) != 0 {
		t.Fatalf("GIWA legacy archive/refund command path restored: %v", offenders)
	}
}

func runtimeFile(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filename
}
