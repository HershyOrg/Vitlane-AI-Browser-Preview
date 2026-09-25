package postgres

import (
	"context"
	"fmt"
	"time"
)

// IntakeGIWAFinalized는 GIWA 수납 확정을 payment 슬림 코어에 합류시킨다
// (ADR-0050·ADR-0055 §4 — payment가 자기 테이블에 쓴다): 중립 CustomerPayment
// (CAPTURED)+FundsReceipt가 곧 Procurement handoff의 durable fact다. PayPal의
// MO별 부분 capture와 달리 tVITUSD는 수수료 0의 선불 rail이므로 주문 수납이
// 먼저 확정되지만, MO position 활성화 시점은 같은 Procurement 경계를 쓴다.
func (r *Repository) IntakeGIWAFinalized(ctx context.Context, now time.Time) (int, error) {
	count := 0
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		paymentRows, err := q.QueryContext(tx, `
			INSERT INTO payment_customer_payments(
				id, agency_order_id, user_id, rail, provider_environment, asset,
				economic_effect, merchant_execution_mode, execution_profile_hash,
				amount_minor, currency, state, version,
				created_at, updated_at
			)
			SELECT gen_random_uuid(), payment.agency_order_id, orders.user_id,
			       orders.payment_rail, orders.provider_environment, orders.asset,
			       orders.economic_effect, orders.merchant_execution_mode,
			       orders.execution_profile_hash,
			       orders.customer_payable_minor, 'USD', 'CAPTURED', 1, $1, $1
			FROM settlement_payments payment
			JOIN agency_orders orders ON orders.id=payment.agency_order_id
			WHERE payment.state='FINALIZED'
			  AND payment.agency_order_id IS NOT NULL
			  AND orders.payment_rail='GIWA'
			  AND orders.provider_environment='TESTNET'
			  AND orders.asset='TVITUSD'
			  AND orders.economic_effect='NO_REAL_VALUE'
			  AND orders.merchant_execution_mode='SIMULATED_NO_EFFECT'
			  AND NOT EXISTS (
			      SELECT 1 FROM payment_customer_payments existing
			      WHERE existing.agency_order_id=payment.agency_order_id
			  )
			RETURNING id::text
		`, now)
		if err != nil {
			return err
		}
		paymentIDs := make([]string, 0)
		for paymentRows.Next() {
			var id string
			if err := paymentRows.Scan(&id); err != nil {
				paymentRows.Close()
				return err
			}
			paymentIDs = append(paymentIDs, id)
		}
		paymentRows.Close()
		if err := paymentRows.Err(); err != nil {
			return err
		}
		count = len(paymentIDs)
		receiptRows, err := q.QueryContext(tx, `
			INSERT INTO payment_funds_receipts(
				id, customer_payment_id, agency_order_id, kind,
				provider_environment, execution_profile_hash,
				amount_minor, currency, accepted,
				occurred_at, created_at, order_hash, pay_tx_hash
			)
			SELECT gen_random_uuid(), pcp.id, pcp.agency_order_id,
			       'GIWA_FINALIZED_PAY', pcp.provider_environment,
			       pcp.execution_profile_hash, pcp.amount_minor, 'USD', TRUE,
			       $1, $1, lower(payment.order_hash), lower(payment.pay_tx_hash)
			FROM payment_customer_payments pcp
			JOIN settlement_payments payment
			  ON payment.agency_order_id=pcp.agency_order_id
			 AND payment.state='FINALIZED'
			WHERE pcp.rail='GIWA' AND pcp.state='CAPTURED'
			  AND payment.pay_tx_hash IS NOT NULL
			  AND NOT EXISTS (
			      SELECT 1 FROM payment_funds_receipts receipt
			      WHERE receipt.customer_payment_id=pcp.id
			  )
			RETURNING id::text
		`, now)
		if err != nil {
			return err
		}
		receiptIDs := make([]string, 0)
		for receiptRows.Next() {
			var id string
			if err := receiptRows.Scan(&id); err != nil {
				receiptRows.Close()
				return err
			}
			receiptIDs = append(receiptIDs, id)
		}
		receiptRows.Close()
		if err := receiptRows.Err(); err != nil {
			return err
		}
		for _, paymentID := range paymentIDs {
			result, err := q.ExecContext(tx, `
				INSERT INTO payment_mo_funding_positions(
					id,allocation_id,agency_order_id,customer_payment_id,
					paypal_authorization_id,rail,source,provider_environment,
					amount_minor,currency,execution_profile_hash,state,version,
					available_at,created_at,updated_at
				)
				SELECT md5(allocation.id::text||':funding')::uuid,allocation.id,
				       allocation.agency_order_id,payment.id,NULL,'GIWA','GIWA_PREPAID',
				       'TESTNET',allocation.customer_gross_minor,'USD',
				       allocation.execution_profile_hash,'AVAILABLE',1,$2,$2,$2
				FROM payment_customer_payments payment
				JOIN agency_order_mo_allocations allocation
				  ON allocation.agency_order_id=payment.agency_order_id
				WHERE payment.id=$1 AND payment.rail='GIWA' AND payment.state='CAPTURED'
			`, paymentID, now)
			if err != nil {
				return err
			}
			if err := EmitMOFundingEventsForPayment(tx, q, paymentID, now); err != nil {
				return err
			}
			created, err := result.RowsAffected()
			if err != nil {
				return err
			}
			var expected int64
			if err := q.QueryRowContext(tx, `
				SELECT count(*) FROM agency_order_mo_allocations allocation
				JOIN payment_customer_payments payment
				  ON payment.agency_order_id=allocation.agency_order_id
				WHERE payment.id=$1
			`, paymentID).Scan(&expected); err != nil {
				return err
			}
			if created == 0 || created != expected {
				return fmt.Errorf("GIWA MO funding allocation incomplete: created=%d expected=%d", created, expected)
			}
		}
		// 수납 합류 사실을 같은 transaction에서 process 이벤트로 발행한다
		// (ADR-0056 §2 — 관찰의 목적지가 "폴링해 가라"에서 "이벤트로 알린다"로).
		for _, id := range paymentIDs {
			if err := EmitCustomerPaymentEvent(tx, q, id, now); err != nil {
				return err
			}
			if err := EmitCustomerFundingReadyEvent(tx, q, id, now); err != nil {
				return err
			}
		}
		for _, id := range receiptIDs {
			if err := EmitFundsReceiptEvent(tx, q, id, now); err != nil {
				return err
			}
		}
		return nil
	})
	return count, err
}
