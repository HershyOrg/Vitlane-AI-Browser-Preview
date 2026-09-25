package postgres

import (
	"testing"
	"time"
)

func TestDefaultConfigIsValid(t *testing.T) {
	config := DefaultConfig()
	if err := config.Validate(); err != nil {
		t.Fatalf("default config: %v", err)
	}
	if config.MaxOpenConnections != 40 || config.MaxIdleConnections != 10 {
		t.Fatalf("pool defaults changed: %#v", config)
	}
	if config.MigrationLockTimeout != 2*time.Minute {
		t.Fatalf("migration lock timeout = %s", config.MigrationLockTimeout)
	}
}

func TestConfigRejectsUnsafePoolAndTimeoutCombinations(t *testing.T) {
	tests := []Config{
		func() Config { c := DefaultConfig(); c.MaxOpenConnections = 0; return c }(),
		func() Config { c := DefaultConfig(); c.MaxOpenConnections = 1; return c }(),
		func() Config { c := DefaultConfig(); c.MaxIdleConnections = 41; return c }(),
		func() Config { c := DefaultConfig(); c.ConnMaxLifetime = time.Second; return c }(),
		func() Config { c := DefaultConfig(); c.MigrationLockTimeout = 0; return c }(),
		func() Config { c := DefaultConfig(); c.MigrationLockTimeout = 11 * time.Minute; return c }(),
		func() Config { c := DefaultConfig(); c.Worker.Transaction = time.Second; return c }(),
	}
	for index, config := range tests {
		if err := config.Validate(); err == nil {
			t.Fatalf("case %d accepted invalid config", index)
		}
	}
}
