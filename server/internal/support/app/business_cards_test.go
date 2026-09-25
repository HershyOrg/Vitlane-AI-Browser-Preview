package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"testing"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	supportapp "github.com/vitlane/vitlane/server/internal/support/app"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

type businessFakeRepository struct {
	*fakeRepository
	attachments map[string]supportdomain.ImageAttachment
}

func (f *businessFakeRepository) CreateMessageWithAttachments(
	ctx context.Context,
	message supportdomain.Message,
	attachments []supportdomain.ImageAttachment,
) (supportdomain.Message, bool, error) {
	var idempotentReplay bool
	for _, existing := range f.messages {
		if existing.IdempotencyKey == message.IdempotencyKey {
			idempotentReplay = true
			break
		}
	}
	if !idempotentReplay && message.BusinessCard != nil && message.BusinessCard.ActionRequired {
		for _, existing := range f.messages {
			if existing.UserID == message.UserID && existing.BusinessCard != nil &&
				existing.BusinessCard.ActionRequired && !f.isResolved(existing.ID) &&
				existing.BusinessCard.Reference == message.BusinessCard.Reference {
				return supportdomain.Message{}, false, supportdomain.ErrActionCardInvalid
			}
		}
	}
	if !idempotentReplay && message.BusinessCard != nil && message.BusinessCard.ResolvesCardID != "" {
		var actionFound bool
		for _, existing := range f.messages {
			if existing.ID == message.BusinessCard.ResolvesCardID &&
				existing.UserID == message.UserID && existing.BusinessCard != nil &&
				existing.BusinessCard.ActionRequired && !f.isResolved(existing.ID) {
				actionFound = true
			}
		}
		if !actionFound {
			return supportdomain.Message{}, false, supportdomain.ErrActionCardInvalid
		}
	}
	created, replay, err := f.CreateMessage(ctx, message)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	if replay {
		for _, attachment := range f.attachments {
			if attachment.MessageID == created.ID {
				created.Attachments = append(created.Attachments, attachment.Metadata())
			}
		}
		return created, true, nil
	}
	if f.attachments == nil {
		f.attachments = make(map[string]supportdomain.ImageAttachment)
	}
	for _, attachment := range attachments {
		f.attachments[attachment.ID] = attachment
		created.Attachments = append(created.Attachments, attachment.Metadata())
	}
	f.messages[len(f.messages)-1] = created
	return created, false, nil
}

func (f *businessFakeRepository) ListLatestByConversationView(
	_ context.Context,
	view supportdomain.ConversationView,
	_ int,
) ([]supportdomain.ConversationHead, error) {
	if len(f.messages) == 0 {
		return nil, nil
	}
	last := f.messages[len(f.messages)-1]
	head := supportdomain.ConversationHead{
		LastMessage: last, ActionRequired: f.hasAction(last.UserID),
	}
	for index := len(f.messages) - 1; index >= 0; index-- {
		message := f.messages[index]
		if message.UserID == last.UserID && message.ContentKind == supportdomain.ContentKindText {
			head.LastOrdinaryMessageID = message.ID
			head.LastOrdinaryAuthor = message.Author
			head.HandledWithoutReply = f.handled != nil &&
				f.handled[message.ID].CustomerMessageID != ""
			break
		}
	}
	switch view {
	case supportdomain.ConversationViewAwaitingReply:
		if !head.AwaitingReply() {
			return nil, nil
		}
	case supportdomain.ConversationViewActionRequired:
		if !head.ActionRequired {
			return nil, nil
		}
	case supportdomain.ConversationViewAll:
	default:
		return nil, supportdomain.ErrQueryInvalid
	}
	return []supportdomain.ConversationHead{head}, nil
}

func (f *businessFakeRepository) HasActionRequired(
	_ context.Context,
	userID string,
) (bool, error) {
	return f.hasAction(userID), nil
}

func (f *businessFakeRepository) CountActionRequired(context.Context) (int, error) {
	users := map[string]bool{}
	for _, message := range f.messages {
		if f.hasAction(message.UserID) {
			users[message.UserID] = true
		}
	}
	return len(users), nil
}

func (f *businessFakeRepository) FindOpenActionCard(
	_ context.Context,
	userID string,
	referenceType string,
	referenceID string,
	idempotencyKey string,
) (string, error) {
	for _, message := range f.messages {
		if message.IdempotencyKey == idempotencyKey && message.UserID == userID &&
			message.BusinessCard != nil &&
			message.BusinessCard.Reference.Type == referenceType &&
			message.BusinessCard.Reference.ID == referenceID &&
			message.BusinessCard.ResolvesCardID != "" {
			return message.BusinessCard.ResolvesCardID, nil
		}
	}
	for _, message := range f.messages {
		if message.UserID == userID && message.BusinessCard != nil &&
			message.BusinessCard.ActionRequired && !f.isResolved(message.ID) &&
			message.BusinessCard.Reference.Type == referenceType &&
			message.BusinessCard.Reference.ID == referenceID {
			return message.ID, nil
		}
	}
	return "", supportdomain.ErrActionCardInvalid
}

func (f *businessFakeRepository) GetImageAttachment(
	_ context.Context,
	attachmentID string,
	userID string,
) (supportdomain.ImageAttachment, error) {
	attachment, ok := f.attachments[attachmentID]
	if !ok || attachment.UserID != userID {
		return supportdomain.ImageAttachment{}, supportdomain.ErrImageAttachmentNotFound
	}
	return attachment, nil
}

func (f *businessFakeRepository) hasAction(userID string) bool {
	for _, message := range f.messages {
		if message.UserID == userID && message.BusinessCard != nil &&
			message.BusinessCard.ActionRequired && !f.isResolved(message.ID) {
			return true
		}
	}
	return false
}

func (f *businessFakeRepository) isResolved(cardID string) bool {
	for _, message := range f.messages {
		if message.BusinessCard != nil && message.BusinessCard.ResolvesCardID == cardID {
			return true
		}
	}
	return false
}

type fakeImageCipher struct{}

func (fakeImageCipher) Encrypt(
	_ context.Context,
	plaintext []byte,
	_ string,
) (supportdomain.EncryptedImage, error) {
	ciphertext := append([]byte(nil), plaintext...)
	for index := range ciphertext {
		ciphertext[index] ^= 0xaa
	}
	return supportdomain.EncryptedImage{
		Ciphertext: ciphertext, Nonce: []byte("test-nonce12"),
		KeyVersion: "test-v1", Fingerprint: "test-fingerprint",
	}, nil
}

func (fakeImageCipher) Decrypt(
	_ context.Context,
	encrypted supportdomain.EncryptedImage,
	_ string,
) ([]byte, error) {
	plaintext := append([]byte(nil), encrypted.Ciphertext...)
	for index := range plaintext {
		plaintext[index] ^= 0xaa
	}
	return plaintext, nil
}

func TestBusinessCardsKeepAwaitingReplyAndActionRequiredSeparate(t *testing.T) {
	base := &fakeRepository{}
	repository := &businessFakeRepository{fakeRepository: base}
	users := fakeUsers{users: map[string]accountdomain.User{
		"u1": {ID: "u1", Email: "u1@example.com"},
	}}
	service := newServiceWithRepository(repository, users)
	service.EnableOrderingGateway(fakeOrdering{owners: map[string]string{"o1": "u1"}})
	service.EnableImageCipher(fakeImageCipher{})

	ordinary, _, err := service.SendCustomerMessageWithImages(
		context.Background(), "u1", "I need help.", "", key,
		[]supportapp.ImageInput{{
			MediaType: supportdomain.ImageMediaTypePNG, Data: tinyPNG(t),
		}},
	)
	if err != nil || len(ordinary.Attachments) != 1 {
		t.Fatalf("ordinary=%+v err=%v", ordinary, err)
	}
	retention := now.Add(30 * 24 * time.Hour)
	request, replay, err := service.PublishBusinessCard(
		context.Background(), supportapp.BusinessCardInput{
			TargetUserID: "u1", Author: supportdomain.AuthorOperator,
			ActorUserID: "op1", Body: "Please confirm the selected color.",
			AgencyOrderID: "o1", IdempotencyKey: "business-card-key-0001",
			Type:          supportdomain.CardProcurementRequest,
			ReferenceType: supportdomain.ReferenceProcurement,
			ReferenceID:   "request-1", ActionRequired: true,
			PublicPayload: json.RawMessage(`{"responseType":"BOOLEAN_CONSENT"}`),
			Images: []supportapp.ImageInput{{
				MediaType: supportdomain.ImageMediaTypePNG,
				Data:      tinyPNG(t), RetentionUntil: &retention,
			}},
		},
	)
	if err != nil || replay || request.BusinessCard == nil ||
		len(request.Attachments) != 1 || request.Author != supportdomain.AuthorOperator {
		t.Fatalf("request=%+v replay=%t err=%v", request, replay, err)
	}

	awaiting, err := service.ListConversations(context.Background(), "AWAITING_REPLY", 10)
	if err != nil || len(awaiting) != 1 || !awaiting[0].AwaitingReply {
		t.Fatalf("awaiting=%+v err=%v", awaiting, err)
	}
	actions, err := service.ListConversations(context.Background(), "ACTION_REQUIRED", 10)
	if err != nil || len(actions) != 1 || !actions[0].ActionRequired {
		t.Fatalf("actions=%+v err=%v", actions, err)
	}
	if _, err := service.HandleWithoutReply(
		context.Background(), "u1", ordinary.ID, "op1",
	); err != nil {
		t.Fatal(err)
	}
	if actions, err = service.ListConversations(
		context.Background(), "ACTION_REQUIRED", 10,
	); err != nil || len(actions) != 1 {
		t.Fatalf("no-reply closed business action: actions=%+v err=%v", actions, err)
	}

	resolutionInput := supportapp.BusinessCardInput{
		TargetUserID: "u1", Author: supportdomain.AuthorCustomer,
		ActorUserID: "u1", Body: "I agree.",
		AgencyOrderID: "o1", IdempotencyKey: "business-card-key-0002",
		Type:          supportdomain.CardProcurementResponse,
		ReferenceType: supportdomain.ReferenceProcurement,
		ReferenceID:   "request-1",
		PublicPayload: json.RawMessage(`{"answer":true}`),
	}
	response, _, err := service.PublishBusinessCardResolution(
		context.Background(), resolutionInput,
	)
	if err != nil || response.BusinessCard == nil ||
		response.BusinessCard.ResolvesCardID != request.ID {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	replayedResponse, replay, err := service.PublishBusinessCardResolution(
		context.Background(), resolutionInput,
	)
	if err != nil || !replay || replayedResponse.ID != response.ID {
		t.Fatalf("resolution replay=%+v replay=%t err=%v", replayedResponse, replay, err)
	}
	secondResolution := resolutionInput
	secondResolution.IdempotencyKey = "business-card-key-0006"
	if _, _, err := service.PublishBusinessCardResolution(
		context.Background(), secondResolution,
	); !errors.Is(err, supportdomain.ErrActionCardInvalid) {
		t.Fatalf("second resolution err=%v", err)
	}
	if actions, err = service.ListConversations(
		context.Background(), "ACTION_REQUIRED", 10,
	); err != nil || len(actions) != 0 {
		t.Fatalf("resolved actions=%+v err=%v", actions, err)
	}
	if _, err := service.HandleWithoutReply(
		context.Background(), "u1", request.ID, "op1",
	); !errors.Is(err, supportdomain.ErrConversationNotAwaiting) {
		t.Fatalf("business card accepted as ordinary no-reply target: %v", err)
	}

	download, err := service.DownloadImage(
		context.Background(), "u1", request.Attachments[0].ID,
	)
	if err != nil || download.MediaType != supportdomain.ImageMediaTypePNG ||
		download.CacheControl != "private, no-store" {
		t.Fatalf("download=%+v err=%v", download, err)
	}
	if _, _, err := image.Decode(bytes.NewReader(download.Data)); err != nil {
		t.Fatalf("downloaded canonical image: %v", err)
	}
	if _, err := service.DownloadImage(
		context.Background(), "u2", request.Attachments[0].ID,
	); !errors.Is(err, supportdomain.ErrImageAttachmentNotFound) {
		t.Fatalf("cross-user image read err=%v", err)
	}
}

func TestBusinessCardImagesFailClosed(t *testing.T) {
	repository := &businessFakeRepository{fakeRepository: &fakeRepository{}}
	users := fakeUsers{users: map[string]accountdomain.User{
		"u1": {ID: "u1", Email: "u1@example.com"},
	}}
	service := newServiceWithRepository(repository, users)
	baseInput := supportapp.BusinessCardInput{
		TargetUserID: "u1", Author: supportdomain.AuthorSystem,
		Body: "Refund status changed.", IdempotencyKey: "business-card-key-0003",
		Type:          supportdomain.CardRefundStatus,
		ReferenceType: supportdomain.ReferenceRefund, ReferenceID: "refund-1",
		PublicPayload: json.RawMessage(`{}`),
		Images: []supportapp.ImageInput{{
			MediaType: supportdomain.ImageMediaTypePNG, Data: tinyPNG(t),
		}},
	}
	if _, _, err := service.PublishBusinessCard(
		context.Background(), baseInput,
	); !errors.Is(err, supportdomain.ErrImageCipherUnavailable) {
		t.Fatalf("missing cipher err=%v", err)
	}
	service.EnableImageCipher(fakeImageCipher{})
	baseInput.IdempotencyKey = "business-card-key-0004"
	baseInput.Images = make([]supportapp.ImageInput, 5)
	for index := range baseInput.Images {
		baseInput.Images[index] = supportapp.ImageInput{
			MediaType: supportdomain.ImageMediaTypePNG, Data: tinyPNG(t),
		}
	}
	if _, _, err := service.PublishBusinessCard(
		context.Background(), baseInput,
	); !errors.Is(err, supportdomain.ErrImageAttachmentInvalid) {
		t.Fatalf("five images err=%v", err)
	}
	baseInput.IdempotencyKey = "business-card-key-0005"
	baseInput.Images = []supportapp.ImageInput{{
		MediaType: supportdomain.ImageMediaTypeJPEG, Data: tinyPNG(t),
	}}
	if _, _, err := service.PublishBusinessCard(
		context.Background(), baseInput,
	); !errors.Is(err, supportdomain.ErrImageAttachmentInvalid) {
		t.Fatalf("mime mismatch err=%v", err)
	}
}

func newServiceWithRepository(
	repository supportapp.Repository,
	users fakeUsers,
) *supportapp.Service {
	return supportapp.NewService(
		repository, users, fakeClock{}, &fakeIDs{}, slog.New(slog.DiscardHandler),
	)
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 0x20, G: 0x70, B: 0xff, A: 0xff})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
