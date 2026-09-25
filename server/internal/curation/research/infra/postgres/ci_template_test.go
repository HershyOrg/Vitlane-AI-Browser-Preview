package postgres

import (
	"github.com/vitlane/vitlane/server/internal/shared/testdb"
	"os"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }
