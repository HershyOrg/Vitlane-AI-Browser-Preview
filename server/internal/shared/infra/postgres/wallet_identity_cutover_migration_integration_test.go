package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

const walletIdentityCutoverMigration = "000031_wallet_identity_kyc_redesign.up.sql"

// TestWalletIdentityCutoverMigration uses an isolated database because the
// migration intentionally deletes preview Wallet/KYC and Purchase data.
func TestWalletIdentityCutoverMigration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	adminDatabase, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := isolatedCutoverDatabaseName(t)
	if _, err := adminDatabase.DB.ExecContext(
		ctx,
		"CREATE DATABASE "+quotePostgresIdentifier(databaseName)+" TEMPLATE template0",
	); err != nil {
		_ = adminDatabase.Close()
		t.Fatalf("create isolated migration database: %v", err)
	}

	var isolatedDatabase *Database
	t.Cleanup(func() {
		if isolatedDatabase != nil {
			_ = isolatedDatabase.Close()
		}
		cleanupContext, cleanupCancel := context.WithTimeout(
			context.Background(), 15*time.Second,
		)
		defer cleanupCancel()
		if _, err := adminDatabase.DB.ExecContext(
			cleanupContext,
			"DROP DATABASE "+quotePostgresIdentifier(databaseName)+" WITH (FORCE)",
		); err != nil {
			t.Errorf("drop isolated migration database: %v", err)
		}
		_ = adminDatabase.Close()
	})

	isolatedDatabase, err = Open(
		ctx, databaseURLWithName(t, databaseURL, databaseName),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertPostgres16(t, ctx, isolatedDatabase.DB)

	directory := migrationTestDirectory(t)
	applyMigrationsOneThroughTwentyTwo(
		t, ctx, isolatedDatabase.DB, directory,
	)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, directory, hardCutoverMigration,
	)
	for _, migration := range []string{
		"000024_agent_auth_control_prepare.up.sql",
		"000025_agent_auth_control_research_cutover.up.sql",
		"000026_agent_auth_control_planning_cutover.up.sql",
		"000027_agent_auth_attempt_cutover.up.sql",
		"000028_agent_connection_revoke_command.up.sql",
		"000029_research_rejected_schema_audit.up.sql",
		"000030_agent_auth_control_legacy_reset.up.sql",
	} {
		applyRecordedMigration(
			t, ctx, isolatedDatabase.DB, directory, migration,
		)
	}
	insertLegacyWalletIdentityFixture(t, ctx, isolatedDatabase)
	insertLegacyDependentPurchaseFixture(t, ctx, isolatedDatabase)
	applyRecordedMigration(
		t, ctx, isolatedDatabase.DB, directory, walletIdentityCutoverMigration,
	)

	assertMigrationRecorded(
		t, ctx, isolatedDatabase.DB, walletIdentityCutoverMigration, true,
	)
	assertTableCount(t, ctx, isolatedDatabase.DB, "wallets", 0)
	assertTableCount(t, ctx, isolatedDatabase.DB, "wallet_registration_attempts", 0)
	assertTableCount(t, ctx, isolatedDatabase.DB, "wallet_ownership_proofs", 0)
	assertTableCount(t, ctx, isolatedDatabase.DB, "kyc_verification_cases", 0)
	assertTableCount(t, ctx, isolatedDatabase.DB, "kyc_provider_operations", 0)
	assertTableCount(t, ctx, isolatedDatabase.DB, "kyc_provider_operation_aliases", 0)
	assertTableCount(t, ctx, isolatedDatabase.DB, "kyc_credentials", 0)
	assertTableCount(t, ctx, isolatedDatabase.DB, "kyc_evidence_observations", 0)
	for _, table := range []string{
		"purchases",
		"checkout_quotes",
		"user_approvals",
		"settlement_authorizations",
		"settlement_payments",
		"settlement_command_outbox",
		"chain_transactions",
		"chain_events",
		"fulfillment_executions",
		"fulfillment_requests",
		"fulfillment_attempts",
		"fulfillment_results",
		"fulfillment_assignments",
		"fulfillment_audit_events",
		"pii_access_audit_events",
		"merchant_shipment_events",
		"purchase_intent_snapshots",
		"receipts",
		"shipping_snapshots",
	} {
		assertTableCount(t, ctx, isolatedDatabase.DB, table, 0)
	}

	var rawCounts []byte
	if err := isolatedDatabase.DB.QueryRowContext(ctx, `
		SELECT row_counts
		FROM wallet_identity_cutover_audits
		WHERE migration_version='000031_wallet_identity_kyc_redesign'
	`).Scan(&rawCounts); err != nil {
		t.Fatalf("read cutover audit: %v", err)
	}
	var counts map[string]any
	if err := json.Unmarshal(rawCounts, &counts); err != nil {
		t.Fatalf("decode cutover audit: %v", err)
	}
	if counts["wallets"] != float64(1) ||
		counts["identityAssurances"] != float64(1) ||
		counts["kycCases"] != float64(1) ||
		counts["purchases"] != float64(1) ||
		counts["checkoutQuotes"] != float64(1) ||
		counts["approvals"] != float64(1) ||
		counts["settlementAuthorizations"] != float64(1) ||
		counts["settlementPayments"] != float64(1) ||
		counts["settlementCommandOutbox"] != float64(1) ||
		counts["chainTransactions"] != float64(1) ||
		counts["chainEvents"] != float64(1) ||
		counts["fulfillmentExecutions"] != float64(1) ||
		counts["fulfillmentRequests"] != float64(1) ||
		counts["fulfillmentAttempts"] != float64(1) ||
		counts["fulfillmentResults"] != float64(1) ||
		counts["fulfillmentAssignments"] != float64(1) ||
		counts["fulfillmentAuditEvents"] != float64(1) ||
		counts["piiAccessAuditEvents"] != float64(1) ||
		counts["merchantShipmentEvents"] != float64(1) ||
		counts["purchaseIntentSnapshots"] != float64(1) ||
		counts["receipts"] != float64(1) ||
		counts["purchaseShippingSnapshots"] != float64(1) {
		t.Fatalf("unexpected cutover audit counts: %#v", counts)
	}

	assertColumnExists(
		t, ctx, isolatedDatabase.DB,
		"user_approvals", "wallet_ownership_proof_id", true,
	)
	assertColumnExists(
		t, ctx, isolatedDatabase.DB,
		"user_approvals", "assurance_id", false,
	)
	assertWalletAddressPrefixConstraint(t, ctx, isolatedDatabase)
}

func assertWalletAddressPrefixConstraint(
	t *testing.T,
	ctx context.Context,
	database *Database,
) {
	t.Helper()
	_, err := database.DB.ExecContext(ctx, `
		INSERT INTO wallets(
			id,user_id,address,address_key,account_id,chain_id,
			registration_status,current_ownership_proof_id,
			registered_at,deregistered_at,created_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000028',
			'24000000-0000-4000-8000-000000000001',
			'zz1111111111111111111111111111111111111111',
			decode(repeat('11',20),'hex'),
			'eip155:91342:zz1111111111111111111111111111111111111111',
			'eip155:91342','DEREGISTERED',NULL,
			'2026-07-29T00:00:00Z','2026-07-29T00:00:00Z',
			'2026-07-29T00:00:00Z','2026-07-29T00:00:00Z'
		)
	`)
	if err == nil {
		t.Fatal("wallet address with a non-0x prefix passed the DB constraint")
	}
}

func insertLegacyWalletIdentityFixture(
	t *testing.T,
	ctx context.Context,
	database *Database,
) {
	t.Helper()
	now := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	const (
		userID      = "24000000-0000-4000-8000-000000000001"
		walletID    = "24000000-0000-4000-8000-000000000002"
		assuranceID = "24000000-0000-4000-8000-000000000003"
		caseID      = "24000000-0000-4000-8000-000000000004"
		auditID     = "24000000-0000-4000-8000-000000000005"
	)
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO users(id,status,email,display_name,created_at,updated_at)
		VALUES ($1,'ACTIVE','cutover@example.test','Cutover Test',$2,$2);

		INSERT INTO wallets(
			id,user_id,address,chain_id,verified_at,created_at,
			ownership_message_hash,is_default,disconnected_at
		) VALUES (
			$3,$1,'0x1111111111111111111111111111111111111111',
			'eip155:91342',$2,$2,decode(repeat('11',32),'hex'),TRUE,NULL
		);

		INSERT INTO identity_assurances(
			id,wallet_id,user_id,level,issuer_ref,schema_ref,evidence_hash,
			verified_at,expires_at,revoked_at,created_at
		) VALUES (
			$4,$3,$1,'WALLET_OWNERSHIP_ONLY',
			'legacy-wallet','legacy-wallet.v1','0xlegacy',
			$2,$2::timestamptz + INTERVAL '24 hours',NULL,$2
		);

		INSERT INTO kyc_verification_cases(
			id,user_id,wallet_id,requested_level,provider_kind,
			external_effect,state,provider_case_ref,assurance_id,
			failure_code,created_at,updated_at,completed_at
		) VALUES (
			$5,$1,$3,'ADVANCED_TEST_KYC','MOCK_DOJANG','SIMULATED',
			'PROVIDER_STARTED','mock-dojang:legacy',NULL,NULL,$2,$2,NULL
		);

		INSERT INTO kyc_verification_audit_events(
			id,case_id,actor_kind,actor_user_id,action,previous_state,
			next_state,reason_code,idempotency_key,details,created_at
		) VALUES (
			$6,$5,'USER',$1,'CASE_CREATED',NULL,'PROVIDER_STARTED',
			NULL,'legacy-cutover-start','{}'::jsonb,$2
		)
	`, userID, now, walletID, assuranceID, caseID, auditID); err != nil {
		t.Fatalf("insert legacy Wallet/KYC fixture: %v", err)
	}
}

func insertLegacyDependentPurchaseFixture(
	t *testing.T,
	ctx context.Context,
	database *Database,
) {
	t.Helper()
	now := time.Date(2026, 7, 29, 0, 1, 0, 0, time.UTC)
	transaction, err := database.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(
		ctx, `SET LOCAL session_replication_role = replica`,
	); err != nil {
		t.Fatalf("disable fixture foreign-key triggers: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO merchant_registry_entries(
			merchant_id,display_name,domain_suffixes,country,currency,
			fulfillment_mode,payment_enabled,principal_recipient,
			registry_version,active,updated_at
		) VALUES (
			'cutover-merchant','Cutover Merchant',ARRAY['cutover.example'],
			'US','USD','MANUAL_MERCHANT_ORDER',TRUE,
			'0x2222222222222222222222222222222222222222',1,TRUE,$2
		);

		INSERT INTO buyer_profiles(
			id,user_id,profile_kind,label,fixture_key,country,city,
			profile_version,snapshot_hash,contains_real_pii,is_default,
			created_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000010',$1,'TEST_PROFILE',
			'Cutover Buyer','cutover-buyer','US','Test City',1,
			'0xbuyer',FALSE,TRUE,$2,$2
		);

		INSERT INTO shipping_profiles(
			id,user_id,label,country,masked_summary,encrypted_payload,
			payload_nonce,key_version,payload_hmac,profile_version,
			is_default,created_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000011',$1,'Cutover Shipping',
			'US','US · ***',decode('01','hex'),decode('02','hex'),
			'test-key','cutover-profile-hmac',1,TRUE,$2,$2
		);

		INSERT INTO shipping_snapshots(
			id,user_id,source_profile_id,profile_version,country,masked_summary,
			encrypted_payload,payload_nonce,key_version,snapshot_hmac,created_at
		) VALUES (
			'24000000-0000-4000-8000-000000000012',$1,
			'24000000-0000-4000-8000-000000000011',1,'US','US · ***',
			decode('03','hex'),decode('04','hex'),'test-key',
			'cutover-snapshot-hmac',$2
		);

		INSERT INTO purchases(
			id,user_id,shopping_session_id,candidate_id,candidate_hash,
			candidate_snapshot,merchant_id,unit_price_amount,currency,quantity,
			purchase_hash,status,configuration_hash,buyer_profile_id,
			buyer_profile_version,buyer_profile_snapshot_hash,
			shipping_snapshot_id,shipping_profile_id,shipping_profile_version,
			shipping_country,shipping_masked_summary,shipping_snapshot_hmac,
			created_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000013',$1,
			'24000000-0000-4000-8000-000000000014',
			'24000000-0000-4000-8000-000000000015','0xcandidate',
			'{
			  "candidateId":"24000000-0000-4000-8000-000000000015",
			  "candidateHash":"0xcandidate",
			  "sessionId":"24000000-0000-4000-8000-000000000014",
			  "providerRef":{"schemaVersion":"provider.v1","provider":"TEST"},
			  "merchantRef":{"canonicalHost":"cutover.example"},
			  "offerRef":{"offerFingerprint":"offer-1"},
			  "purchasePath":{
			    "schemaVersion":"vitlane.purchase-path.v1","reasonCodes":[]
			  },
			  "variantDiscovery":{
			    "schemaVersion":"vitlane.variant-discovery.v1",
			    "fields":[],"providerVariantRefs":[]
			  },
			  "candidateConfiguration":{
			    "schemaVersion":"vitlane.candidate-configuration.v1",
			    "configurationHash":"configuration-1",
			    "fields":[],"selections":[]
			  },
			  "variantResolution":{"configurationHash":"configuration-1"},
			  "evidenceHash":"0xevidence",
			  "observedAt":"2026-07-29T00:00:00Z"
			}'::jsonb,
			'cutover-merchant',50,'USD',1,'0xpurchase','COMPLETED',
			'configuration-1',
			'24000000-0000-4000-8000-000000000010',1,'0xbuyer',
			'24000000-0000-4000-8000-000000000012',
			'24000000-0000-4000-8000-000000000011',1,'US','US · ***',
			'cutover-snapshot-hmac',$2,$2
		);

		INSERT INTO checkout_quotes(
			id,purchase_id,user_id,quote_hash,unit_price_amount,quantity,
			item_subtotal_amount,shipping_amount,tax_amount,discount_amount,
			payable_total_amount,currency,settlement_amount_base_units,
			token_address,token_decimals,settlement_address,chain_id,
			merchant_registry_version,fee_bps,fee_recipient,
			principal_recipient,expires_at,created_at
		) VALUES (
			'24000000-0000-4000-8000-000000000016',
			'24000000-0000-4000-8000-000000000013',$1,'0xquote',
			50,1,50,0,0,0,50,'USD',50000000,
			'0x3333333333333333333333333333333333333333',6,
			'0x4444444444444444444444444444444444444444',91342,1,100,
			'0x5555555555555555555555555555555555555555',
			'0x2222222222222222222222222222222222222222',
			$2::timestamptz + INTERVAL '15 minutes',$2
		);

		INSERT INTO user_approvals(
			id,purchase_id,quote_id,user_id,wallet_id,assurance_id,
			approval_hash,acknowledged_test_asset,acknowledged_no_legal_sale,
			approved_at,purchase_hash,quote_hash,approved_amount,
			approved_currency,policy_id,policy_version,idempotency_key
		) VALUES (
			'24000000-0000-4000-8000-000000000017',
			'24000000-0000-4000-8000-000000000013',
			'24000000-0000-4000-8000-000000000016',$1,
			'24000000-0000-4000-8000-000000000002',
			'24000000-0000-4000-8000-000000000003',
			'0xapproval',TRUE,TRUE,$2,'0xpurchase','0xquote',50,'USD',
			'PHASE5_TEST_SETTLEMENT','2026-07-24','cutover-approval'
		);

		INSERT INTO settlement_authorizations(
			id,purchase_id,order_hash,payer,nonce,pay_deadline,refund_after,
			signer_address,typed_data_hash,authorization_payload,created_at
		) VALUES (
			'24000000-0000-4000-8000-000000000018',
			'24000000-0000-4000-8000-000000000013','0xorder',
			'0x1111111111111111111111111111111111111111',1,
			$2::timestamptz + INTERVAL '15 minutes',
			$2::timestamptz + INTERVAL '1 hour',
			'0x6666666666666666666666666666666666666666',
			'0xtyped','{}'::jsonb,$2
		);

		INSERT INTO settlement_payments(
			id,purchase_id,order_hash,chain_id,settlement_address,payer,
			amount_base_units,complete_tx_hash,state,safe_block,finalized_block,
			created_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000019',
			'24000000-0000-4000-8000-000000000013','0xorder',91342,
			'0x4444444444444444444444444444444444444444',
			'0x1111111111111111111111111111111111111111',50000000,
			'0xcomplete','COMPLETED',10,10,$2,$2
		);

		INSERT INTO settlement_command_outbox(
			settlement_payment_id,chain_id,purpose,signer_address,order_hash,
			fulfillment_hash,state,attempt_count,planned_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000019',91342,'COMPLETE',
			'0x6666666666666666666666666666666666666666','0xorder',
			'0xfulfillment','PLANNED',0,$2,$2
		);

		INSERT INTO chain_transactions(
			chain_id,tx_hash,settlement_payment_id,purpose,state,
			submitted_at,updated_at
		) VALUES (
			91342,'0xcomplete',
			'24000000-0000-4000-8000-000000000019','COMPLETE',
			'FINALIZED',$2,$2
		);

		INSERT INTO chain_events(
			chain_id,tx_hash,log_index,block_number,block_hash,event_name,
			order_hash,observed_at
		) VALUES (
			91342,'0xcomplete',0,10,'0xblock','OrderCompleted','0xorder',$2
		);

		INSERT INTO fulfillment_requests(
			id,settlement_payment_id,purchase_id,quote_id,schema_version,
			fulfillment_spec_hash,request_hash,payload,state,created_at,updated_at
		) VALUES (
			'24000000-0000-4000-8000-000000000020',
			'24000000-0000-4000-8000-000000000019',
			'24000000-0000-4000-8000-000000000013',
			'24000000-0000-4000-8000-000000000016',
			'vitlane.fulfillment-request.v1','0xspec','0xrequest',
			'{}'::jsonb,'TERMINAL',$2,$2
		);

		INSERT INTO fulfillment_attempts(
			id,request_id,attempt_number,adapter_kind,processing_mode,
			actor_kind,actor_user_id,idempotency_key,state,started_at,completed_at
		) VALUES (
			'24000000-0000-4000-8000-000000000021',
			'24000000-0000-4000-8000-000000000020',1,
			'MOCK_MERCHANT_ORDER','MANUAL_OPERATOR','USER',$1,
			'cutover-fulfillment-attempt','SUCCEEDED',$2,$2
		);

		INSERT INTO fulfillment_results(
			id,request_id,attempt_id,schema_version,outcome,result_kind,
			merchant_order_created,external_order_reference,failure_code,
			result_hash,payload,created_at,external_effect,
			merchant_order_flow_completed,mock_order_reference
		) VALUES (
			'24000000-0000-4000-8000-000000000022',
			'24000000-0000-4000-8000-000000000020',
			'24000000-0000-4000-8000-000000000021',
			'vitlane.fulfillment-result.v1','ORDER_ACCEPTED',
			'MOCK_MERCHANT_ORDER',FALSE,NULL,NULL,'0xresult','{}'::jsonb,$2,
			'SIMULATED',TRUE,'mock-cutover-order'
		);

		INSERT INTO fulfillment_executions(
			id,settlement_payment_id,merchant_id,mode,state,result_hash,
			created_at,updated_at,processing_mode,operator_user_id,handled_at,
			quote_id,quote_hash,quote_snapshot,idempotency_key,result_payload,
			eligible_at,merchant_order_created,external_order_reference,
			request_id,external_effect,adapter_kind,
			merchant_order_flow_completed,mock_order_reference
		) VALUES (
			'24000000-0000-4000-8000-000000000023',
			'24000000-0000-4000-8000-000000000019','cutover-merchant',
			'MANUAL_MERCHANT_ORDER','ORDER_ACCEPTED','0xexecution',$2,$2,
			'MANUAL_OPERATOR',$1,$2,
			'24000000-0000-4000-8000-000000000016','0xquote','{}'::jsonb,
			'cutover-fulfillment-execution','{}'::jsonb,$2,FALSE,NULL,
			'24000000-0000-4000-8000-000000000020','SIMULATED',
			'MOCK_MERCHANT_ORDER',TRUE,'mock-cutover-order'
		);

		INSERT INTO fulfillment_assignments(
			fulfillment_request_id,operator_user_id,state,assigned_at
		) VALUES (
			'24000000-0000-4000-8000-000000000020',$1,'ACTIVE',$2
		);

		INSERT INTO fulfillment_audit_events(
			id,actor_kind,actor_user_id,action,settlement_payment_id,
			idempotency_key,details,created_at
		) VALUES (
			'24000000-0000-4000-8000-000000000024','USER',$1,
			'OPERATOR_ASSIGNED','24000000-0000-4000-8000-000000000019',
			'cutover-fulfillment-audit','{}'::jsonb,$2
		);

		INSERT INTO pii_access_audit_events(
			id,purchase_id,shipping_snapshot_id,actor_user_id,action,
			reason_code,reason_detail,outcome,correlation_id,idempotency_key,
			event_hash,created_at
		) VALUES (
			'24000000-0000-4000-8000-000000000025',
			'24000000-0000-4000-8000-000000000013',
			'24000000-0000-4000-8000-000000000012',$1,
			'SHIPPING_ADDRESS_REVEAL','PLACE_MERCHANT_ORDER',
			'cutover fixture access','GRANTED','cutover-correlation',
			'cutover-pii-audit','0xpii',$2
		);

		INSERT INTO merchant_shipment_events(
			id,fulfillment_request_id,sequence_number,state,provider_event_ref,
			external_effect,payload_hash,occurred_at,recorded_at
		) VALUES (
			'24000000-0000-4000-8000-000000000026',
			'24000000-0000-4000-8000-000000000020',1,'ORDER_ACCEPTED',
			'cutover-shipment','SIMULATED','0xshipment',$2,$2
		);

		INSERT INTO purchase_intent_snapshots(
			purchase_id,schema_version,provider_ref,merchant_ref,offer_ref,
			purchase_path,variant_discovery,candidate_configuration,
			variant_resolution,configuration_hash,created_at
		) VALUES (
			'24000000-0000-4000-8000-000000000013',
			'vitlane.purchase-intent.v2',
			'{"schemaVersion":"provider.v1","provider":"TEST"}'::jsonb,
			'{"displayName":"Cutover","canonicalHost":"cutover.example"}'::jsonb,
			'{"offerFingerprint":"offer-1"}'::jsonb,
			'{
			  "schemaVersion":"vitlane.purchase-path.v1",
			  "providerKind":"TEST","executionMode":"TEST",
			  "externalEffect":"SIMULATED","liveOrderability":"NO",
			  "settlementStatus":"TEST","status":"AVAILABLE","reasonCodes":[]
			}'::jsonb,
			'{
			  "schemaVersion":"vitlane.variant-discovery.v1",
			  "fields":[],"providerVariantRefs":[]
			}'::jsonb,
			'{
			  "schemaVersion":"vitlane.candidate-configuration.v1",
			  "configurationHash":"configuration-1",
			  "fields":[],"selections":[]
			}'::jsonb,
			'{
			  "configurationHash":"configuration-1",
			  "resolvedBy":"PROVIDER","status":"PROVIDER_VERIFIED"
			}'::jsonb,
			'configuration-1',$2
		);

		INSERT INTO receipts(
			id,purchase_id,settlement_payment_id,kind,legal_sale,
			merchant_of_record,refund_handler,terminal_tx_hash,
			receipt_hash,payload,created_at
		) VALUES (
			'24000000-0000-4000-8000-000000000027',
			'24000000-0000-4000-8000-000000000013',
			'24000000-0000-4000-8000-000000000019','TEST',FALSE,
			'MERCHANT','NOT_APPLICABLE','0xcomplete','0xreceipt',
			'{}'::jsonb,$2
		)
	`, "24000000-0000-4000-8000-000000000001", now); err != nil {
		t.Fatalf("insert legacy dependent Purchase fixture: %v", err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatalf("commit legacy dependent Purchase fixture: %v", err)
	}
}
