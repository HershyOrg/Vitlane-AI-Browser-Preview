package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestFulfillmentAuditActionRewriteHappensWithoutTheOldConstraint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		operations []string
	}{
		{
			name: "up",
			path: "../../../../migrations/" +
				"000016_production_core_simulated_adapters.up.sql",
			operations: []string{
				"UPDATE fulfillment_audit_events\n" +
					"SET action='MERCHANT_ORDER_ACCEPTED'\n" +
					"WHERE action='PURCHASE_ASSUMED';",
			},
		},
		{
			name: "down",
			path: "../../../../migrations/" +
				"000016_production_core_simulated_adapters.down.sql",
			operations: []string{
				"UPDATE fulfillment_audit_events\n" +
					"SET action='PURCHASE_ASSUMED'\n" +
					"WHERE action='MERCHANT_ORDER_ACCEPTED';",
				"DELETE FROM fulfillment_audit_events\n" +
					"WHERE action IN ('OPERATOR_ASSIGNED', 'MERCHANT_ORDER_UNKNOWN');",
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			body, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatal(err)
			}
			sql := string(body)
			drop := strings.Index(sql, `ALTER TABLE fulfillment_audit_events
    DROP CONSTRAINT fulfillment_audit_events_action_check;`)
			add := strings.Index(sql, `ALTER TABLE fulfillment_audit_events
    ADD CONSTRAINT fulfillment_audit_events_action_check`)
			if drop < 0 || add < 0 || drop >= add {
				t.Fatal("action constraint must be dropped before it is replaced")
			}
			for _, operation := range test.operations {
				index := strings.Index(sql, operation)
				if index <= drop || index >= add {
					t.Fatalf(
						"operation must run after the old constraint is dropped "+
							"and before the replacement is added: %q",
						operation,
					)
				}
			}
		})
	}
}
