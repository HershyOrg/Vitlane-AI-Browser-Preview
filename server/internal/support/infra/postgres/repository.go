// Package postgres는 Support 대화(ADR-0059)의 저장소다. 멱등 발신은
// INSERT … ON CONFLICT DO NOTHING 후 재조회(marketing 전례), 읽음은 set-based
// 워터마크, 운영자 목록은 DISTINCT ON으로 대화별 마지막 메시지를 뽑는다.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

const messageColumns = `
	id, user_id, author, body,
	COALESCE(agency_order_id::text,''), COALESCE(created_by_user_id::text,''),
	COALESCE(idempotency_key,''), created_at, customer_read_at,
	idempotency_namespace,
	content_kind, COALESCE(business_card_type,''),
	COALESCE(business_reference_type,''), COALESCE(business_reference_id,''),
	action_required, COALESCE(resolves_card_id::text,''),
	COALESCE(public_payload::text,'{}'), COALESCE(request_hash,'')`

type Repository struct{ database *sharedpostgres.Database }

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

func (r *Repository) CreateMessage(
	ctx context.Context,
	message supportdomain.Message,
) (supportdomain.Message, bool, error) {
	return r.CreateMessageWithAttachments(ctx, message, nil)
}

func (r *Repository) CreateMessageWithAttachments(
	ctx context.Context,
	message supportdomain.Message,
	attachments []supportdomain.ImageAttachment,
) (supportdomain.Message, bool, error) {
	hashes := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		hashes = append(hashes, attachment.ContentSHA256)
	}
	if message.RequestHash == "" || len(attachments) > 0 {
		message.RequestHash = supportdomain.CanonicalRequestHash(message, hashes)
	}
	var created supportdomain.Message
	var replay bool
	err := r.database.WithinTransaction(ctx, func(txCtx context.Context) error {
		if message.BusinessCard != nil && message.BusinessCard.ResolvesCardID != "" {
			existing, found, findErr := r.findExistingByIdempotencyKey(
				txCtx, message.IdempotencyNamespace, message.IdempotencyKey,
			)
			if findErr != nil {
				return findErr
			}
			if found {
				if !sameIdempotentRequest(existing, message) {
					return supportdomain.ErrIdempotencyKeyReused
				}
				created, replay = existing, true
				return nil
			}
			claimed, claimErr := r.claimResolutionTarget(txCtx, message)
			if claimErr != nil {
				return claimErr
			}
			if !claimed {
				existing, found, findErr = r.findExistingByIdempotencyKey(
					txCtx, message.IdempotencyNamespace, message.IdempotencyKey,
				)
				if findErr != nil {
					return findErr
				}
				if found && sameIdempotentRequest(existing, message) {
					created, replay = existing, true
					return nil
				}
				if found {
					return supportdomain.ErrIdempotencyKeyReused
				}
				return supportdomain.ErrActionCardInvalid
			}
		}
		cardType, referenceType, referenceID, resolvesCardID := "", "", "", ""
		var publicPayload any
		if message.BusinessCard != nil {
			cardType = message.BusinessCard.Type
			referenceType = message.BusinessCard.Reference.Type
			referenceID = message.BusinessCard.Reference.ID
			resolvesCardID = message.BusinessCard.ResolvesCardID
			publicPayload = string(message.BusinessCard.PublicPayload)
		}
		result, err := r.database.Queryer(txCtx).ExecContext(txCtx, `
			INSERT INTO support_messages(
				id, user_id, author, body, agency_order_id, created_by_user_id,
				idempotency_key, idempotency_namespace, created_at, content_kind,
				business_card_type, business_reference_type,
				business_reference_id, action_required, resolves_card_id,
				public_payload, request_hash
			) VALUES(
				$1, $2, $3, $4, NULLIF($5,'')::uuid, NULLIF($6,'')::uuid,
				$7, $8, $9, $10, NULLIF($11,''), NULLIF($12,''), NULLIF($13,''),
				$14, NULLIF($15,'')::uuid, $16::jsonb, $17
			)
			ON CONFLICT (idempotency_namespace, idempotency_key)
			WHERE idempotency_key IS NOT NULL DO NOTHING
		`, message.ID, message.UserID, message.Author, message.Body,
			message.AgencyOrderID, message.CreatedByUserID,
			message.IdempotencyKey, message.IdempotencyNamespace,
			message.CreatedAt, message.ContentKind,
			cardType, referenceType, referenceID,
			message.BusinessCard != nil && message.BusinessCard.ActionRequired,
			resolvesCardID, publicPayload, message.RequestHash)
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) &&
				postgresError.ConstraintName == "uq_support_messages_resolves_card" {
				return supportdomain.ErrActionCardInvalid
			}
			return fmt.Errorf("insert support message: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			existing, findErr := r.findByIdempotencyKey(
				txCtx, message.IdempotencyNamespace, message.IdempotencyKey,
			)
			if findErr != nil {
				return findErr
			}
			if !sameIdempotentRequest(existing, message) {
				return supportdomain.ErrIdempotencyKeyReused
			}
			created, replay = existing, true
			return nil
		}
		if err := r.insertAttachments(txCtx, message, attachments); err != nil {
			return err
		}
		if err := r.insertOpenAction(txCtx, message); err != nil {
			return err
		}
		message.Attachments = attachmentMetadata(attachments)
		created = message
		return nil
	})
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	return created, replay, nil
}

func (r *Repository) claimResolutionTarget(
	ctx context.Context,
	message supportdomain.Message,
) (bool, error) {
	if message.BusinessCard == nil || message.BusinessCard.ResolvesCardID == "" {
		return true, nil
	}
	var claimedID string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		DELETE FROM support_open_business_actions
		WHERE card_message_id=$1::uuid AND user_id=$2::uuid
		  AND reference_type=$3 AND reference_id=$4
		RETURNING card_message_id::text
	`, message.BusinessCard.ResolvesCardID, message.UserID,
		message.BusinessCard.Reference.Type,
		message.BusinessCard.Reference.ID).Scan(&claimedID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim support action card: %w", err)
	}
	return claimedID == message.BusinessCard.ResolvesCardID, nil
}

func (r *Repository) insertOpenAction(
	ctx context.Context,
	message supportdomain.Message,
) error {
	if message.BusinessCard == nil || !message.BusinessCard.ActionRequired {
		return nil
	}
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO support_open_business_actions(
			user_id, reference_type, reference_id, card_message_id, opened_at
		) VALUES($1,$2,$3,$4,$5)
	`, message.UserID, message.BusinessCard.Reference.Type,
		message.BusinessCard.Reference.ID, message.ID, message.CreatedAt)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			(postgresError.ConstraintName == "support_open_business_actions_pkey" ||
				postgresError.ConstraintName == "support_open_business_actions_card_message_id_key") {
			return supportdomain.ErrActionCardInvalid
		}
		return fmt.Errorf("open support action card: %w", err)
	}
	return nil
}

func (r *Repository) insertAttachments(
	ctx context.Context,
	message supportdomain.Message,
	attachments []supportdomain.ImageAttachment,
) error {
	if len(attachments) > supportdomain.MaxImagesPerMessage {
		return supportdomain.ErrImageAttachmentInvalid
	}
	for _, attachment := range attachments {
		if attachment.MessageID != message.ID || attachment.UserID != message.UserID {
			return supportdomain.ErrImageAttachmentInvalid
		}
		_, err := r.database.Queryer(ctx).ExecContext(ctx, `
			INSERT INTO support_image_attachments(
				id, message_id, user_id, ordinal, media_type, width, height,
				byte_size, content_sha256, ciphertext, nonce, key_version,
				ciphertext_fingerprint, created_at, retention_until,
				legal_hold, legal_hold_reason_hash, purged_at
			) VALUES(
				$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
				$16,NULLIF($17,''),$18
			)
		`, attachment.ID, attachment.MessageID, attachment.UserID,
			attachment.Ordinal, attachment.MediaType, attachment.Width,
			attachment.Height, attachment.ByteSize, attachment.ContentSHA256,
			attachment.Encrypted.Ciphertext, attachment.Encrypted.Nonce,
			attachment.Encrypted.KeyVersion, attachment.Encrypted.Fingerprint,
			attachment.CreatedAt, attachment.RetentionUntil, attachment.LegalHold,
			attachment.LegalHoldReasonHash, attachment.PurgedAt)
		if err != nil {
			return fmt.Errorf("insert support image attachment: %w", err)
		}
	}
	return nil
}

func (r *Repository) findByIdempotencyKey(
	ctx context.Context,
	namespace string,
	key string,
) (supportdomain.Message, error) {
	message, found, err := r.findExistingByIdempotencyKey(ctx, namespace, key)
	if err != nil {
		return supportdomain.Message{}, err
	}
	if !found {
		return supportdomain.Message{}, fmt.Errorf("idempotent support message disappeared")
	}
	return message, nil
}

func (r *Repository) findExistingByIdempotencyKey(
	ctx context.Context,
	namespace string,
	key string,
) (supportdomain.Message, bool, error) {
	message, err := scanMessage(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+messageColumns+`
		FROM support_messages
		WHERE idempotency_namespace=$1 AND idempotency_key=$2
	`, namespace, key))
	if errors.Is(err, sql.ErrNoRows) {
		return supportdomain.Message{}, false, nil
	}
	if err == nil {
		messages := []supportdomain.Message{message}
		if loadErr := r.loadAttachmentMetadata(ctx, messages); loadErr != nil {
			return supportdomain.Message{}, false, loadErr
		}
		message = messages[0]
	}
	return message, err == nil, err
}

func (r *Repository) ListMessages(
	ctx context.Context,
	userID string,
	limit int,
	beforeID string,
) ([]supportdomain.Message, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+messageColumns+`
		FROM support_messages
		WHERE user_id=$1
		  AND (
			$2 = ''
			OR (created_at, id) < (
				SELECT anchor.created_at, anchor.id FROM support_messages anchor
				WHERE anchor.id=$2::uuid AND anchor.user_id=$1
			)
		  )
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, userID, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("list support messages: %w", err)
	}
	defer rows.Close()
	messages, err := collectMessages(rows)
	if err != nil {
		return nil, err
	}
	if err := r.loadAttachmentMetadata(ctx, messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (r *Repository) CountCustomerMessagesSince(
	ctx context.Context,
	userID string,
	since time.Time,
) (int, error) {
	var count int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT count(*)::int FROM support_messages
		WHERE user_id=$1 AND author='CUSTOMER' AND created_at>$2
	`, userID, since).Scan(&count)
	return count, err
}

func (r *Repository) MarkAllRead(
	ctx context.Context,
	userID string,
	now time.Time,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE support_messages
		SET customer_read_at=$1
		WHERE user_id=$2 AND author<>'CUSTOMER' AND customer_read_at IS NULL
	`, now, userID)
	return err
}

func (r *Repository) CountUnread(ctx context.Context, userID string) (int, error) {
	var count int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT count(*)::int FROM support_messages
		WHERE user_id=$1 AND author<>'CUSTOMER' AND customer_read_at IS NULL
	`, userID).Scan(&count)
	return count, err
}

func (r *Repository) ListLatestByConversation(
	ctx context.Context,
	awaitingOnly bool,
	limit int,
) ([]supportdomain.ConversationHead, error) {
	view := supportdomain.ConversationViewAll
	if awaitingOnly {
		view = supportdomain.ConversationViewAwaitingReply
	}
	return r.ListLatestByConversationView(ctx, view, limit)
}

func (r *Repository) ListLatestByConversationView(
	ctx context.Context,
	view supportdomain.ConversationView,
	limit int,
) ([]supportdomain.ConversationHead, error) {
	predicate := ""
	switch view {
	case supportdomain.ConversationViewAwaitingReply:
		predicate = " WHERE ordinary.author='CUSTOMER' AND handled.customer_message_id IS NULL"
	case supportdomain.ConversationViewActionRequired:
		predicate = ` WHERE EXISTS (
			SELECT 1 FROM support_open_business_actions action
			WHERE action.user_id=last.user_id
		)`
	case supportdomain.ConversationViewAll:
	default:
		return nil, supportdomain.ErrQueryInvalid
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+qualifiedMessageColumns("last")+`,
		       COALESCE(ordinary.id::text,''), COALESCE(ordinary.author,''),
		       handled.customer_message_id IS NOT NULL,
		       EXISTS (
			 SELECT 1 FROM support_open_business_actions action
			 WHERE action.user_id=last.user_id
		       )
		FROM (
			SELECT DISTINCT ON (user_id) *
			FROM support_messages
			ORDER BY user_id, created_at DESC, id DESC
		) last
		LEFT JOIN LATERAL (
			SELECT id, author
			FROM support_messages ordinary_message
			WHERE ordinary_message.user_id=last.user_id
			  AND ordinary_message.content_kind='TEXT'
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		) ordinary ON TRUE
		LEFT JOIN support_no_reply_resolutions handled
		  ON handled.customer_message_id=ordinary.id`+predicate+`
		ORDER BY last.created_at DESC, last.id DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list support conversations: %w", err)
	}
	defer rows.Close()
	heads, err := collectConversationHeads(rows)
	if err != nil {
		return nil, err
	}
	messages := make([]supportdomain.Message, 0, len(heads))
	for _, head := range heads {
		messages = append(messages, head.LastMessage)
	}
	if err := r.loadAttachmentMetadata(ctx, messages); err != nil {
		return nil, err
	}
	for index := range heads {
		heads[index].LastMessage = messages[index]
	}
	return heads, nil
}

func (r *Repository) GetAwaitingCustomerMessageID(ctx context.Context, userID string) (string, error) {
	var messageID string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT last.id::text
		FROM (
			SELECT id, author
			FROM support_messages
			WHERE user_id=$1 AND content_kind='TEXT'
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		) last
		LEFT JOIN support_no_reply_resolutions handled
		  ON handled.customer_message_id=last.id
		WHERE last.author='CUSTOMER' AND handled.customer_message_id IS NULL
	`, userID).Scan(&messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read support conversation state: %w", err)
	}
	return messageID, nil
}

func (r *Repository) HasActionRequired(ctx context.Context, userID string) (bool, error) {
	var actionRequired bool
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM support_open_business_actions action
			WHERE action.user_id=$1
		)
	`, userID).Scan(&actionRequired)
	if err != nil {
		return false, fmt.Errorf("read support action-required state: %w", err)
	}
	return actionRequired, nil
}

func (r *Repository) MarkHandledWithoutReply(
	ctx context.Context,
	userID string,
	resolution supportdomain.NoReplyResolution,
) (bool, error) {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO support_no_reply_resolutions(
			customer_message_id, handled_by_user_id, handled_at
		)
		SELECT latest.id, $3::uuid, $4
		FROM (
			SELECT id, author
			FROM support_messages
			WHERE user_id=$1 AND content_kind='TEXT'
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		) latest
		WHERE latest.id=$2::uuid AND latest.author='CUSTOMER'
		ON CONFLICT (customer_message_id) DO NOTHING
	`, userID, resolution.CustomerMessageID, resolution.HandledByUserID,
		resolution.HandledAt)
	if err != nil {
		return false, fmt.Errorf("handle support conversation without reply: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 1 {
		return false, nil
	}

	// ON CONFLICT로 기다린 동시 재시도도 다음 statement의 새 snapshot에서
	// 기존 row를 확인한다. 그 사이 새 고객 메시지가 왔다면 old resolution을
	// replay로 성공시키지 않고 stale action으로 거절한다.
	var replay bool
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM support_no_reply_resolutions handled
			JOIN support_messages message
			  ON message.id=handled.customer_message_id
			WHERE handled.customer_message_id=$2::uuid
			  AND message.user_id=$1
			  AND message.author='CUSTOMER'
			  AND NOT EXISTS (
				SELECT 1 FROM support_messages newer
				WHERE newer.user_id=message.user_id
				  AND newer.content_kind='TEXT'
				  AND (newer.created_at, newer.id) > (message.created_at, message.id)
			  )
		)
	`, userID, resolution.CustomerMessageID).Scan(&replay)
	if err != nil {
		return false, fmt.Errorf("read support no-reply resolution: %w", err)
	}
	if !replay {
		return false, supportdomain.ErrConversationNotAwaiting
	}
	return true, nil
}

func (r *Repository) CountAwaiting(ctx context.Context) (int, error) {
	var count int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT count(*)::int FROM (
			SELECT DISTINCT ON (user_id) id, author
			FROM support_messages
			WHERE content_kind='TEXT'
			ORDER BY user_id, created_at DESC, id DESC
		) last
		LEFT JOIN support_no_reply_resolutions handled
		  ON handled.customer_message_id=last.id
		WHERE last.author='CUSTOMER' AND handled.customer_message_id IS NULL
	`).Scan(&count)
	return count, err
}

func (r *Repository) CountActionRequired(ctx context.Context) (int, error) {
	var count int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT count(DISTINCT action.user_id)::int
		FROM support_open_business_actions action
	`).Scan(&count)
	return count, err
}

func (r *Repository) FindOpenActionCard(
	ctx context.Context,
	userID string,
	referenceType string,
	referenceID string,
	idempotencyKey string,
) (string, error) {
	userID = strings.TrimSpace(userID)
	referenceType = strings.TrimSpace(referenceType)
	referenceID = strings.TrimSpace(referenceID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if userID == "" || referenceType == "" || referenceID == "" {
		return "", supportdomain.ErrActionCardInvalid
	}
	var cardMessageID string
	if idempotencyKey != "" {
		err := r.database.Queryer(ctx).QueryRowContext(ctx, `
			SELECT resolves_card_id::text
			FROM support_messages
			WHERE idempotency_key=$1 AND user_id=$2::uuid
			  AND content_kind='BUSINESS_CARD'
			  AND business_reference_type=$3 AND business_reference_id=$4
			  AND resolves_card_id IS NOT NULL
		`, idempotencyKey, userID, referenceType, referenceID).Scan(&cardMessageID)
		if err == nil {
			return cardMessageID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("find replayed support action card: %w", err)
		}
	}
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT card_message_id::text
		FROM support_open_business_actions
		WHERE user_id=$1::uuid AND reference_type=$2 AND reference_id=$3
	`, userID, referenceType, referenceID).Scan(&cardMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", supportdomain.ErrActionCardInvalid
	}
	if err != nil {
		return "", fmt.Errorf("find open support action card: %w", err)
	}
	return cardMessageID, nil
}

type scanner interface{ Scan(...any) error }

type messageRows interface {
	scanner
	Next() bool
	Err() error
}

func scanMessage(row scanner) (supportdomain.Message, error) {
	var message supportdomain.Message
	var readAt sql.NullTime
	var cardType, referenceType, referenceID, resolvesCardID string
	var actionRequired bool
	var publicPayload []byte
	targets := messageScanTargets(
		&message, &readAt, &cardType, &referenceType, &referenceID,
		&actionRequired, &resolvesCardID, &publicPayload,
	)
	err := row.Scan(targets...)
	hydrateMessage(
		&message, readAt, cardType, referenceType, referenceID,
		actionRequired, resolvesCardID, publicPayload,
	)
	return message, err
}

func collectMessages(rows messageRows) ([]supportdomain.Message, error) {
	messages := make([]supportdomain.Message, 0)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan support message: %w", err)
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func collectConversationHeads(rows messageRows) ([]supportdomain.ConversationHead, error) {
	heads := make([]supportdomain.ConversationHead, 0)
	for rows.Next() {
		var head supportdomain.ConversationHead
		var readAt sql.NullTime
		var cardType, referenceType, referenceID, resolvesCardID string
		var actionRequired bool
		var publicPayload []byte
		targets := messageScanTargets(
			&head.LastMessage, &readAt, &cardType, &referenceType,
			&referenceID, &actionRequired, &resolvesCardID, &publicPayload,
		)
		targets = append(targets,
			&head.LastOrdinaryMessageID, &head.LastOrdinaryAuthor,
			&head.HandledWithoutReply, &head.ActionRequired,
		)
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("scan support conversation: %w", err)
		}
		hydrateMessage(
			&head.LastMessage, readAt, cardType, referenceType, referenceID,
			actionRequired, resolvesCardID, publicPayload,
		)
		heads = append(heads, head)
	}
	return heads, rows.Err()
}

func messageScanTargets(
	message *supportdomain.Message,
	readAt *sql.NullTime,
	cardType, referenceType, referenceID *string,
	actionRequired *bool,
	resolvesCardID *string,
	publicPayload *[]byte,
) []any {
	return []any{
		&message.ID, &message.UserID, &message.Author, &message.Body,
		&message.AgencyOrderID, &message.CreatedByUserID,
		&message.IdempotencyKey, &message.CreatedAt, readAt,
		&message.IdempotencyNamespace,
		&message.ContentKind, cardType, referenceType, referenceID,
		actionRequired, resolvesCardID, publicPayload, &message.RequestHash,
	}
}

func hydrateMessage(
	message *supportdomain.Message,
	readAt sql.NullTime,
	cardType, referenceType, referenceID string,
	actionRequired bool,
	resolvesCardID string,
	publicPayload []byte,
) {
	if readAt.Valid {
		message.ReadAt = &readAt.Time
	}
	if message.ContentKind != supportdomain.ContentKindBusinessCard {
		return
	}
	if message.BusinessCard == nil {
		message.BusinessCard = &supportdomain.BusinessCard{}
	}
	message.BusinessCard.Type = cardType
	message.BusinessCard.Reference = supportdomain.BusinessReference{
		Type: referenceType, ID: referenceID,
	}
	message.BusinessCard.ResolvesCardID = resolvesCardID
	message.BusinessCard.ActionRequired = actionRequired
	message.BusinessCard.PublicPayload = append(json.RawMessage(nil), publicPayload...)
}

func qualifiedMessageColumns(alias string) string {
	columns := []string{
		"id", "user_id", "author", "body",
		"COALESCE(agency_order_id::text,'')",
		"COALESCE(created_by_user_id::text,'')",
		"COALESCE(idempotency_key,'')", "created_at", "customer_read_at",
		"idempotency_namespace",
		"content_kind", "COALESCE(business_card_type,'')",
		"COALESCE(business_reference_type,'')",
		"COALESCE(business_reference_id,'')", "action_required",
		"COALESCE(resolves_card_id::text,'')",
		"COALESCE(public_payload::text,'{}')", "COALESCE(request_hash,'')",
	}
	for index, column := range columns {
		if strings.HasPrefix(column, "COALESCE(") {
			columns[index] = strings.Replace(column, "(", "("+alias+".", 1)
			continue
		}
		columns[index] = alias + "." + column
	}
	return strings.Join(columns, ", ")
}

func sameIdempotentRequest(existing, requested supportdomain.Message) bool {
	if existing.RequestHash != "" && requested.RequestHash != "" {
		return existing.RequestHash == requested.RequestHash
	}
	return existing.UserID == requested.UserID &&
		existing.Author == requested.Author && existing.Body == requested.Body &&
		existing.AgencyOrderID == requested.AgencyOrderID &&
		existing.CreatedByUserID == requested.CreatedByUserID &&
		existing.ContentKind == requested.ContentKind
}

func attachmentMetadata(
	attachments []supportdomain.ImageAttachment,
) []supportdomain.ImageAttachmentMetadata {
	if len(attachments) == 0 {
		return nil
	}
	metadata := make([]supportdomain.ImageAttachmentMetadata, 0, len(attachments))
	for _, attachment := range attachments {
		metadata = append(metadata, attachment.Metadata())
	}
	return metadata
}

func (r *Repository) loadAttachmentMetadata(
	ctx context.Context,
	messages []supportdomain.Message,
) error {
	if len(messages) == 0 {
		return nil
	}
	ids := make([]string, 0, len(messages))
	positions := make(map[string]int, len(messages))
	for index, message := range messages {
		ids = append(ids, message.ID)
		positions[message.ID] = index
	}
	encodedIDs, _ := json.Marshal(ids)
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT message_id::text, id::text, ordinal, media_type, width, height,
		       byte_size, content_sha256, created_at, retention_until, legal_hold
		FROM support_image_attachments
		WHERE message_id IN (
			SELECT value::uuid FROM jsonb_array_elements_text($1::jsonb)
		)
		ORDER BY message_id, ordinal
	`, string(encodedIDs))
	if err != nil {
		return fmt.Errorf("list support image attachments: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var messageID string
		var ordinal int
		var attachment supportdomain.ImageAttachment
		var retention sql.NullTime
		if err := rows.Scan(
			&messageID, &attachment.ID, &ordinal, &attachment.MediaType,
			&attachment.Width, &attachment.Height, &attachment.ByteSize,
			&attachment.ContentSHA256, &attachment.CreatedAt, &retention,
			&attachment.LegalHold,
		); err != nil {
			return fmt.Errorf("scan support image attachment metadata: %w", err)
		}
		attachment.Ordinal = ordinal
		if retention.Valid {
			attachment.RetentionUntil = &retention.Time
		}
		if index, ok := positions[messageID]; ok {
			messages[index].Attachments = append(
				messages[index].Attachments, attachment.Metadata(),
			)
		}
	}
	return rows.Err()
}

func (r *Repository) GetImageAttachment(
	ctx context.Context,
	attachmentID string,
	userID string,
) (supportdomain.ImageAttachment, error) {
	var attachment supportdomain.ImageAttachment
	var retention sql.NullTime
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id::text, message_id::text, user_id::text, ordinal, media_type,
		       width, height, byte_size, content_sha256, ciphertext, nonce,
		       key_version, ciphertext_fingerprint, created_at,
		       retention_until, legal_hold, COALESCE(legal_hold_reason_hash,'')
		FROM support_image_attachments
		WHERE id=$1::uuid AND user_id=$2::uuid AND purged_at IS NULL
	`, attachmentID, userID).Scan(
		&attachment.ID, &attachment.MessageID, &attachment.UserID,
		&attachment.Ordinal, &attachment.MediaType, &attachment.Width,
		&attachment.Height, &attachment.ByteSize, &attachment.ContentSHA256,
		&attachment.Encrypted.Ciphertext, &attachment.Encrypted.Nonce,
		&attachment.Encrypted.KeyVersion, &attachment.Encrypted.Fingerprint,
		&attachment.CreatedAt, &retention, &attachment.LegalHold,
		&attachment.LegalHoldReasonHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return supportdomain.ImageAttachment{}, supportdomain.ErrImageAttachmentNotFound
	}
	if err != nil {
		return supportdomain.ImageAttachment{}, fmt.Errorf("read support image attachment: %w", err)
	}
	if retention.Valid {
		attachment.RetentionUntil = &retention.Time
	}
	return attachment, nil
}
