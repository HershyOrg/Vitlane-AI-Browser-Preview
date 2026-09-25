package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
	supportpostgres "github.com/vitlane/vitlane/server/internal/support/infra/postgres"
)

const (
	customerID = "00000000-0000-4000-8000-00000000c001"
	operatorID = "00000000-0000-4000-8000-00000000c002"
)

func TestPostgresSupportMessages(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	database, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx, "../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	lock, err := database.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended('vitlane.integration_tests', 0))`); err != nil {
		t.Fatal(err)
	}
	defer lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended('vitlane.integration_tests', 0))`)
	if _, err := database.DB.ExecContext(ctx, `
		TRUNCATE support_image_attachments, support_open_business_actions,
		         support_no_reply_resolutions, support_messages
	`); err != nil {
		t.Fatal(err)
	}
	// support_messages.user_id는 users FK다 — 대화 소유 고객과 운영자를 시드한다.
	for _, seed := range [][2]string{
		{customerID, "customer@example.com"},
		{operatorID, "operator@example.com"},
	} {
		if _, err := database.DB.ExecContext(ctx, `
			INSERT INTO users(id, status, email, display_name, created_at, updated_at)
			VALUES ($1,'ACTIVE',$2,'지원 테스트',now(),now())
			ON CONFLICT (id) DO NOTHING
		`, seed[0], seed[1]); err != nil {
			t.Fatal(err)
		}
	}

	repository := supportpostgres.NewRepository(database)
	base := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)

	first, err := supportdomain.NewCustomerMessage(
		"00000000-0000-4000-8000-00000000a001", customerID,
		"배송이 언제 되나요?", "", "support-key-000001", base,
	)
	if err != nil {
		t.Fatal(err)
	}
	created, replay, err := repository.CreateMessage(ctx, first)
	if err != nil || replay {
		t.Fatalf("created=%+v replay=%t err=%v", created, replay, err)
	}
	replayed, replay, err := repository.CreateMessage(ctx, first)
	if err != nil || !replay || replayed.ID != first.ID {
		t.Fatalf("replayed=%+v replay=%t err=%v", replayed, replay, err)
	}
	conflicting, err := supportdomain.NewCustomerMessage(
		"00000000-0000-4000-8000-00000000a002", customerID,
		"다른 내용", "", "support-key-000001", base.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.CreateMessage(ctx, conflicting); err != supportdomain.ErrIdempotencyKeyReused {
		t.Fatalf("reused key err=%v", err)
	}

	// 대기 파생: 최신 ordinary TEXT가 고객 발신이다.
	awaiting, err := repository.CountAwaiting(ctx)
	if err != nil || awaiting != 1 {
		t.Fatalf("awaiting=%d err=%v", awaiting, err)
	}
	latest, err := repository.ListLatestByConversation(ctx, true, 10)
	if err != nil || len(latest) != 1 || latest[0].LastMessage.ID != first.ID {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	if messageID, stateErr := repository.GetAwaitingCustomerMessageID(ctx, customerID); stateErr != nil || messageID != first.ID {
		t.Fatalf("initial awaiting message=%q err=%v", messageID, stateErr)
	}
	resolution, err := supportdomain.NewNoReplyResolution(first.ID, operatorID, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	replay, err = repository.MarkHandledWithoutReply(ctx, customerID, resolution)
	if err != nil || replay {
		t.Fatalf("handle first replay=%t err=%v", replay, err)
	}
	if replay, err = repository.MarkHandledWithoutReply(ctx, customerID, resolution); err != nil || !replay {
		t.Fatalf("handle replay=%t err=%v", replay, err)
	}
	if awaiting, err = repository.CountAwaiting(ctx); err != nil || awaiting != 0 {
		t.Fatalf("handled awaiting=%d err=%v", awaiting, err)
	}
	if messageID, stateErr := repository.GetAwaitingCustomerMessageID(ctx, customerID); stateErr != nil || messageID != "" {
		t.Fatalf("handled awaiting message=%q err=%v", messageID, stateErr)
	}
	if latest, err = repository.ListLatestByConversation(ctx, true, 10); err != nil || len(latest) != 0 {
		t.Fatalf("handled latest=%+v err=%v", latest, err)
	}
	if unread, unreadErr := repository.CountUnread(ctx, customerID); unreadErr != nil || unread != 0 {
		t.Fatalf("handled unread=%d err=%v", unread, unreadErr)
	}
	unchanged, err := repository.ListMessages(ctx, customerID, 10, "")
	if err != nil || len(unchanged) != 1 || unchanged[0].Author != supportdomain.AuthorCustomer ||
		unchanged[0].Body != first.Body {
		t.Fatalf("handled message changed=%+v err=%v", unchanged, err)
	}

	secondCustomer, err := supportdomain.NewCustomerMessage(
		"00000000-0000-4000-8000-00000000a002", customerID,
		"추가 문의입니다.", "", "support-key-000003", base.Add(90*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.CreateMessage(ctx, secondCustomer); err != nil {
		t.Fatal(err)
	}
	if awaiting, err = repository.CountAwaiting(ctx); err != nil || awaiting != 1 {
		t.Fatalf("new customer awaiting=%d err=%v", awaiting, err)
	}
	if messageID, stateErr := repository.GetAwaitingCustomerMessageID(ctx, customerID); stateErr != nil || messageID != secondCustomer.ID {
		t.Fatalf("new customer awaiting message=%q err=%v", messageID, stateErr)
	}
	statusCard, err := supportdomain.NewBusinessCard(
		supportdomain.CardDeliveryDelay, supportdomain.ReferenceDelivery,
		"delivery-status-1", false, "", json.RawMessage(`{"status":"DELAYED"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	statusMessage, err := supportdomain.NewSystemBusinessCardMessage(
		"00000000-0000-4000-8000-00000000b010", customerID,
		"Delivery status updated.", "", "support-card-key-0010",
		statusCard, base.Add(105*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.CreateMessage(ctx, statusMessage); err != nil {
		t.Fatal(err)
	}
	if latest, err = repository.ListLatestByConversation(ctx, true, 10); err != nil ||
		len(latest) != 1 || latest[0].LastMessage.ID != statusMessage.ID ||
		latest[0].AwaitingCustomerMessageID() != secondCustomer.ID {
		t.Fatalf("business-card awaiting latest=%+v err=%v", latest, err)
	}
	if messageID, stateErr := repository.GetAwaitingCustomerMessageID(ctx, customerID); stateErr != nil || messageID != secondCustomer.ID {
		t.Fatalf("business-card awaiting message=%q err=%v", messageID, stateErr)
	}
	if _, err = repository.MarkHandledWithoutReply(ctx, customerID, resolution); err != supportdomain.ErrConversationNotAwaiting {
		t.Fatalf("stale resolution err=%v", err)
	}

	reply, err := supportdomain.NewOperatorMessage(
		"00000000-0000-4000-8000-00000000a003", customerID, operatorID,
		"곧 발송됩니다.", "", "support-key-000002", base.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.CreateMessage(ctx, reply); err != nil {
		t.Fatal(err)
	}

	// 운영자 답변 후: 대기 해제 + SYSTEM 카드와 운영자 답변 안 읽음 2.
	if awaiting, err = repository.CountAwaiting(ctx); err != nil || awaiting != 0 {
		t.Fatalf("awaiting=%d err=%v", awaiting, err)
	}
	if latest, err = repository.ListLatestByConversation(ctx, false, 10); err != nil ||
		len(latest) != 1 || latest[0].LastMessage.ID != reply.ID {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	unread, err := repository.CountUnread(ctx, customerID)
	if err != nil || unread != 2 {
		t.Fatalf("unread=%d err=%v", unread, err)
	}
	if err := repository.MarkAllRead(ctx, customerID, base.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if unread, err = repository.CountUnread(ctx, customerID); err != nil || unread != 0 {
		t.Fatalf("unread=%d err=%v", unread, err)
	}

	// 목록: 최신순 + before 커서.
	messages, err := repository.ListMessages(ctx, customerID, 10, "")
	if err != nil || len(messages) != 4 || messages[0].ID != reply.ID {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if messages[0].ReadAt == nil {
		t.Fatalf("watermark missing: %+v", messages[0])
	}
	older, err := repository.ListMessages(ctx, customerID, 10, reply.ID)
	if err != nil || len(older) != 3 || older[0].ID != statusMessage.ID ||
		older[1].ID != secondCustomer.ID || older[2].ID != first.ID {
		t.Fatalf("older=%+v err=%v", older, err)
	}

	count, err := repository.CountCustomerMessagesSince(ctx, customerID, base.Add(-time.Hour))
	if err != nil || count != 2 {
		t.Fatalf("customer count=%d err=%v", count, err)
	}

	// Typed cards use the same conversation but a separate action-required
	// queue. SYSTEM/OPERATOR cards do not close or reopen ordinary reply state.
	actionCard, err := supportdomain.NewBusinessCard(
		supportdomain.CardRefundRequest, supportdomain.ReferenceRefund,
		"refund-request-1", true, "", json.RawMessage(`{"reason":"DAMAGED"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	action, err := supportdomain.NewCustomerBusinessCardMessage(
		"00000000-0000-4000-8000-00000000b001", customerID,
		"The item arrived damaged.", "", "support-card-key-0001",
		actionCard, base.Add(4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	attachment := supportdomain.ImageAttachment{
		ID:        "00000000-0000-4000-8000-00000000d001",
		MessageID: action.ID, UserID: customerID, Ordinal: 1,
		MediaType: supportdomain.ImageMediaTypeJPEG,
		Width:     10, Height: 20, ByteSize: 123,
		ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Encrypted: supportdomain.EncryptedImage{
			Ciphertext: []byte("ciphertext"), Nonce: []byte("nonce-value"),
			KeyVersion: "test-v1", Fingerprint: "fingerprint-value",
		},
		CreatedAt: base.Add(4 * time.Minute),
	}
	createdAction, replay, err := repository.CreateMessageWithAttachments(
		ctx, action, []supportdomain.ImageAttachment{attachment},
	)
	if err != nil || replay || len(createdAction.Attachments) != 1 {
		t.Fatalf("created action=%+v replay=%t err=%v", createdAction, replay, err)
	}
	replayedAction, replay, err := repository.CreateMessageWithAttachments(
		ctx, action, []supportdomain.ImageAttachment{attachment},
	)
	if err != nil || !replay || replayedAction.ID != action.ID ||
		len(replayedAction.Attachments) != 1 {
		t.Fatalf("replayed action=%+v replay=%t err=%v", replayedAction, replay, err)
	}
	conflictingAttachment := attachment
	conflictingAttachment.ContentSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, _, err := repository.CreateMessageWithAttachments(
		ctx, action, []supportdomain.ImageAttachment{conflictingAttachment},
	); !errors.Is(err, supportdomain.ErrIdempotencyKeyReused) {
		t.Fatalf("image idempotency conflict err=%v", err)
	}
	if actionCount, actionErr := repository.CountActionRequired(ctx); actionErr != nil || actionCount != 1 {
		t.Fatalf("action count=%d err=%v", actionCount, actionErr)
	}
	if openID, findErr := repository.FindOpenActionCard(
		ctx, customerID, supportdomain.ReferenceRefund, "refund-request-1", "",
	); findErr != nil || openID != action.ID {
		t.Fatalf("open action=%q err=%v", openID, findErr)
	}
	duplicateAction := action
	duplicateAction.ID = "00000000-0000-4000-8000-00000000b004"
	duplicateAction.IdempotencyKey = "support-card-key-0004"
	duplicateAction.RequestHash = ""
	if _, _, err := repository.CreateMessage(ctx, duplicateAction); !errors.Is(err, supportdomain.ErrActionCardInvalid) {
		t.Fatalf("duplicate open reference err=%v", err)
	}
	if heads, listErr := repository.ListLatestByConversationView(
		ctx, supportdomain.ConversationViewActionRequired, 10,
	); listErr != nil || len(heads) != 1 || !heads[0].ActionRequired ||
		len(heads[0].LastMessage.Attachments) != 1 {
		t.Fatalf("action heads=%+v err=%v", heads, listErr)
	}
	if awaiting, awaitingErr := repository.CountAwaiting(ctx); awaitingErr != nil || awaiting != 0 {
		t.Fatalf("business card changed ordinary awaiting=%d err=%v", awaiting, awaitingErr)
	}
	if _, err := repository.MarkHandledWithoutReply(
		ctx, customerID, supportdomain.NoReplyResolution{
			CustomerMessageID: action.ID, HandledByUserID: operatorID,
			HandledAt: base.Add(5 * time.Minute),
		},
	); !errors.Is(err, supportdomain.ErrConversationNotAwaiting) {
		t.Fatalf("business card accepted by no-reply resolution: %v", err)
	}
	storedAttachment, err := repository.GetImageAttachment(ctx, attachment.ID, customerID)
	if err != nil || string(storedAttachment.Encrypted.Ciphertext) != "ciphertext" {
		t.Fatalf("stored attachment=%+v err=%v", storedAttachment, err)
	}
	if _, err := repository.GetImageAttachment(ctx, attachment.ID, operatorID); !errors.Is(err, supportdomain.ErrImageAttachmentNotFound) {
		t.Fatalf("cross-owner attachment err=%v", err)
	}

	resolutionCard, err := supportdomain.NewBusinessCard(
		supportdomain.CardRefundDecision, supportdomain.ReferenceRefund,
		"refund-request-1", false, action.ID,
		json.RawMessage(`{"decision":"APPROVED"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := supportdomain.NewOperatorBusinessCardMessage(
		"00000000-0000-4000-8000-00000000b002", customerID, operatorID,
		"Your refund request was approved.", "", "support-card-key-0002",
		resolutionCard, base.Add(6*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.CreateMessage(ctx, decision); err != nil {
		t.Fatal(err)
	}
	if actionCount, actionErr := repository.CountActionRequired(ctx); actionErr != nil || actionCount != 0 {
		t.Fatalf("resolved action count=%d err=%v", actionCount, actionErr)
	}
	if _, findErr := repository.FindOpenActionCard(
		ctx, customerID, supportdomain.ReferenceRefund, "refund-request-1", "",
	); !errors.Is(findErr, supportdomain.ErrActionCardInvalid) {
		t.Fatalf("resolved reference still open: %v", findErr)
	}
	if _, _, err := repository.CreateMessage(ctx, decision); err != nil {
		t.Fatalf("decision replay: %v", err)
	}
	if replayTarget, findErr := repository.FindOpenActionCard(
		ctx, customerID, supportdomain.ReferenceRefund, "refund-request-1",
		"support-card-key-0002",
	); findErr != nil || replayTarget != action.ID {
		t.Fatalf("resolution replay target=%q err=%v", replayTarget, findErr)
	}
	secondResolution, err := supportdomain.NewSystemBusinessCardMessage(
		"00000000-0000-4000-8000-00000000b003", customerID,
		"Duplicate resolution.", "", "support-card-key-0003",
		resolutionCard, base.Add(7*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.CreateMessage(ctx, secondResolution); !errors.Is(err, supportdomain.ErrActionCardInvalid) {
		t.Fatalf("second action resolution err=%v", err)
	}

	// A caller-controlled ordinary message key must not reserve the trusted
	// owner-fact BusinessCard namespace. Both rows persist and replay against
	// their own namespace even when the raw key is identical.
	namespacedCard, err := supportdomain.NewBusinessCard(
		supportdomain.CardRefundStatus, supportdomain.ReferenceRefund,
		"refund-namespace-test", false, "", json.RawMessage(`{"state":"SUCCEEDED"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	namespacedMessage, err := supportdomain.NewSystemBusinessCardMessage(
		"00000000-0000-4000-8000-00000000a009", customerID,
		"Refund completed.", "", first.IdempotencyKey, namespacedCard,
		base.Add(8*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	createdCard, replay, err := repository.CreateMessage(ctx, namespacedMessage)
	if err != nil || replay || createdCard.ID != namespacedMessage.ID {
		t.Fatalf("namespaced card=%+v replay=%t err=%v", createdCard, replay, err)
	}
	replayedCard, replay, err := repository.CreateMessage(ctx, namespacedMessage)
	if err != nil || !replay || replayedCard.ID != namespacedMessage.ID {
		t.Fatalf("namespaced replay=%+v replay=%t err=%v", replayedCard, replay, err)
	}
}
