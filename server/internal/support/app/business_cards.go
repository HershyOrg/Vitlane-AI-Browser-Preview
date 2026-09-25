package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

type BusinessCardInput struct {
	TargetUserID   string
	Author         string
	ActorUserID    string
	Body           string
	AgencyOrderID  string
	IdempotencyKey string
	Type           string
	ReferenceType  string
	ReferenceID    string
	ActionRequired bool
	ResolvesCardID string
	PublicPayload  json.RawMessage
	Images         []ImageInput
}

// PublishBusinessCardResolution lets an owning product resolve the exact open
// Support action using the owner reference it already knows. The repository
// resolves that reference to one card ID; PublishBusinessCard then performs an
// exact-ID compare-and-set so concurrent resolutions cannot both succeed.
func (s *Service) PublishBusinessCardResolution(
	ctx context.Context,
	input BusinessCardInput,
) (supportdomain.Message, bool, error) {
	if input.ActionRequired || strings.TrimSpace(input.ResolvesCardID) != "" {
		return supportdomain.Message{}, false, supportdomain.ErrActionCardInvalid
	}
	business, ok := s.repository.(BusinessRepository)
	if !ok {
		return supportdomain.Message{}, false, supportdomain.ErrActionCardInvalid
	}
	actionCardID, err := business.FindOpenActionCard(
		ctx, strings.TrimSpace(input.TargetUserID),
		strings.TrimSpace(input.ReferenceType), strings.TrimSpace(input.ReferenceID),
		strings.TrimSpace(input.IdempotencyKey),
	)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	input.ResolvesCardID = actionCardID
	return s.PublishBusinessCard(ctx, input)
}

// PublishBusinessCard appends a typed communication projection to the same
// user conversation used by ordinary Support text. Owning Procurement,
// Payment and Logistics services must complete their own command first and
// publish the resulting fact here; this method does not execute those facts.
func (s *Service) PublishBusinessCard(
	ctx context.Context,
	input BusinessCardInput,
) (supportdomain.Message, bool, error) {
	input.TargetUserID = strings.TrimSpace(input.TargetUserID)
	input.ActorUserID = strings.TrimSpace(input.ActorUserID)
	input.AgencyOrderID = strings.TrimSpace(input.AgencyOrderID)
	if _, found, err := s.resolveContact(ctx, input.TargetUserID); err != nil {
		return supportdomain.Message{}, false, err
	} else if !found {
		return supportdomain.Message{}, false, supportdomain.ErrUserNotFound
	}
	if input.AgencyOrderID != "" {
		if err := s.verifyOrderOwner(
			ctx, input.AgencyOrderID, input.TargetUserID,
		); err != nil {
			return supportdomain.Message{}, false, err
		}
	}
	card, err := supportdomain.NewBusinessCard(
		input.Type, input.ReferenceType, input.ReferenceID,
		input.ActionRequired, input.ResolvesCardID, input.PublicPayload,
	)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	now := s.clock.Now()
	messageID := s.ids.NewID()
	var message supportdomain.Message
	switch strings.ToUpper(strings.TrimSpace(input.Author)) {
	case supportdomain.AuthorCustomer:
		if input.ActorUserID != input.TargetUserID {
			return supportdomain.Message{}, false, supportdomain.ErrMessageInvalid
		}
		message, err = supportdomain.NewCustomerBusinessCardMessage(
			messageID, input.TargetUserID, input.Body, input.AgencyOrderID,
			input.IdempotencyKey, card, now,
		)
	case supportdomain.AuthorOperator:
		message, err = supportdomain.NewOperatorBusinessCardMessage(
			messageID, input.TargetUserID, input.ActorUserID, input.Body,
			input.AgencyOrderID, input.IdempotencyKey, card, now,
		)
	case supportdomain.AuthorSystem:
		if input.ActorUserID != "" {
			return supportdomain.Message{}, false, supportdomain.ErrMessageInvalid
		}
		message, err = supportdomain.NewSystemBusinessCardMessage(
			messageID, input.TargetUserID, input.Body, input.AgencyOrderID,
			input.IdempotencyKey, card, now,
		)
	default:
		err = supportdomain.ErrMessageInvalid
	}
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	if message.Author == supportdomain.AuthorCustomer {
		count, countErr := s.repository.CountCustomerMessagesSince(
			ctx, message.UserID, now.Add(-time.Hour),
		)
		if countErr != nil {
			return supportdomain.Message{}, false, countErr
		}
		if count >= customerHourlyLimit {
			return supportdomain.Message{}, false, supportdomain.ErrTooManyMessages
		}
	}
	attachments, err := s.prepareImages(
		ctx, message.ID, message.UserID, input.Images, now,
	)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	hashes := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		hashes = append(hashes, attachment.ContentSHA256)
	}
	message.RequestHash = supportdomain.CanonicalRequestHash(message, hashes)
	businessRepository, ok := s.repository.(BusinessRepository)
	if !ok {
		return supportdomain.Message{}, false, supportdomain.ErrBusinessCardInvalid
	}
	created, replay, err := businessRepository.CreateMessageWithAttachments(
		ctx, message, attachments,
	)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	s.logger.InfoContext(ctx, "support business card published",
		"event", "support.business_card.published",
		"message_id", created.ID,
		"card_type", card.Type,
		"author", created.Author,
		"action_required", card.ActionRequired,
		"image_count", len(created.Attachments),
		"replay", replay,
	)
	return created, replay, nil
}
