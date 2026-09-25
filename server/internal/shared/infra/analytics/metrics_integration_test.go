package analytics

import (
	"context"
	"github.com/vitlane/vitlane/server/internal/shared/testdb"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }
func TestBusinessMetricsUseExistingRecordsWithoutConsent(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("requires dedicated PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db := testdb.Open(t, ctx, base, "../../../../migrations")
	load := func(name string) string {
		b, err := os.ReadFile(filepath.Join("../../../../../analytics/sql", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	start, end := "2026-09-01T00:00:00+09:00", "2026-10-01T00:00:00+09:00"
	// Compile against the actual complete migrated schema first.
	for _, name := range []string{"business-usage.sql", "transactions.sql"} {
		rows, err := db.DB.QueryContext(ctx, load(name), start, end, "{}")
		if err != nil {
			t.Fatal(name, err)
		}
		rows.Close()
	}
	rows, err := db.DB.QueryContext(ctx, load("business-cohorts.sql"), end, "{}")
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	// Isolated projected fixtures retain actual column types, without manufacturing
	// entire payment pipelines. The outer transaction is always rolled back.
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, table := range []string{"curations", "curation_conversation_requests", "agency_orders", "payment_mo_cash_receipts", "payment_mo_compensations", "merchant_orders"} {
		if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE "+table+" AS SELECT * FROM public."+table+" WITH NO DATA"); err != nil {
			t.Fatal(err)
		}
	}
	_, err = tx.ExecContext(ctx, `
 INSERT INTO curations(id,user_id,created_at) VALUES
 ('10000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000001','2026-08-31 15:00:00Z'),
 ('10000000-0000-4000-8000-000000000002','20000000-0000-4000-8000-000000000002','2026-09-20 00:00:00Z'),
 ('10000000-0000-4000-8000-000000000003','20000000-0000-4000-8000-000000000003','2026-09-01 00:00:00Z'),
 ('10000000-0000-4000-8000-000000000004','20000000-0000-4000-8000-000000000004','2026-09-30 15:00:00Z');
 INSERT INTO curation_conversation_requests(user_id,created_at) VALUES
 ('20000000-0000-4000-8000-000000000001','2026-09-09 00:00:00Z');
 INSERT INTO agency_orders(id,user_id,issued_at,payment_rail,provider_environment,economic_effect,merchant_execution_mode,currency,customer_payable_minor) VALUES
 ('30000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000001','2026-09-10Z','PAYPAL','LIVE','REAL_MONEY','LIVE_MERCHANT_EFFECT','USD',100),
 ('30000000-0000-4000-8000-000000000002','20000000-0000-4000-8000-000000000002','2026-09-21Z','PAYPAL','SANDBOX','NO_REAL_VALUE','SIMULATED_NO_EFFECT','USD',200);
 INSERT INTO payment_mo_cash_receipts(agency_order_id,occurred_at,currency,gross_minor) VALUES
 ('30000000-0000-4000-8000-000000000001','2026-09-10Z','USD',60),
 ('30000000-0000-4000-8000-000000000001','2026-09-11Z','USD',40),
 ('30000000-0000-4000-8000-000000000002','2026-09-21Z','USD',200);
 INSERT INTO payment_mo_compensations(agency_order_id,state,action,completed_at,currency,amount_minor) VALUES
 ('30000000-0000-4000-8000-000000000001','SUCCEEDED','VOID','2026-09-12Z','USD',999),
 ('30000000-0000-4000-8000-000000000001','SUCCEEDED','REFUND','2026-09-12Z','USD',10),
 ('30000000-0000-4000-8000-000000000001','EXECUTION_PENDING','REFUND','2026-09-12Z','USD',90);
 `)
	if err != nil {
		t.Fatal(err)
	}
	excluded := "{20000000-0000-4000-8000-000000000003}"
	var active, curations, requests, orders int
	if err := tx.QueryRowContext(ctx, load("business-usage.sql"), start, end, excluded).Scan(&active, &curations, &requests, &orders); err != nil {
		t.Fatal(err)
	}
	if active != 2 || curations != 2 || requests != 1 || orders != 2 {
		t.Fatalf("counts=%d/%d/%d/%d", active, curations, requests, orders)
	}
	var month time.Time
	var cohort, w1eligible, w1returned, d30eligible, d30returned int
	if err := tx.QueryRowContext(ctx, load("business-cohorts.sql"), end, excluded).Scan(&month, &cohort, &w1eligible, &w1returned, &d30eligible, &d30returned); err != nil {
		t.Fatal(err)
	}
	if cohort != 2 || w1eligible != 1 || w1returned != 1 || d30eligible != 0 || d30returned != 0 {
		t.Fatalf("cohort=%d/%d/%d/%d/%d", cohort, w1eligible, w1returned, d30eligible, d30returned)
	}
	rows, err = tx.QueryContext(ctx, load("transactions.sql"), start, end, excluded)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var rail, environment, economic, merchant, currency, metric, amount string
		var count int
		if err := rows.Scan(&rail, &environment, &economic, &merchant, &currency, &metric, &count, &amount); err != nil {
			t.Fatal(err)
		}
		values[environment+":"+metric] = amount
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if values["LIVE:paypal_captures"] != "100" || values["SANDBOX:paypal_captures"] != "200" || values["LIVE:completed_refunds"] != "10" {
		t.Fatal(values)
	}
}
