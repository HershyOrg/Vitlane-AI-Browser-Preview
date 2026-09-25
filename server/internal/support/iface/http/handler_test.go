package http

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	supportapp "github.com/vitlane/vitlane/server/internal/support/app"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

type testClock struct{}

func (testClock) Now() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }

type testIDs struct{}

func (testIDs) NewID() string { return "00000000-0000-4000-8000-000000000001" }

type testRepository struct {
	messages      []supportdomain.Message
	customerCount int
	handled       supportdomain.NoReplyResolution
}

type testAttachmentRepository struct {
	*testRepository
	attachments map[string]supportdomain.ImageAttachment
}

func (r *testAttachmentRepository) CreateMessageWithAttachments(
	ctx context.Context,
	message supportdomain.Message,
	attachments []supportdomain.ImageAttachment,
) (supportdomain.Message, bool, error) {
	created, replay, err := r.CreateMessage(ctx, message)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	if r.attachments == nil {
		r.attachments = make(map[string]supportdomain.ImageAttachment)
	}
	for _, attachment := range attachments {
		r.attachments[attachment.ID] = attachment
		created.Attachments = append(created.Attachments, attachment.Metadata())
	}
	r.messages[len(r.messages)-1] = created
	return created, replay, nil
}

func (r *testAttachmentRepository) GetImageAttachment(
	_ context.Context,
	attachmentID string,
	userID string,
) (supportdomain.ImageAttachment, error) {
	attachment, ok := r.attachments[attachmentID]
	if !ok || attachment.UserID != userID {
		return supportdomain.ImageAttachment{}, supportdomain.ErrImageAttachmentNotFound
	}
	return attachment, nil
}

type testImageCipher struct{}

func (testImageCipher) Encrypt(
	_ context.Context,
	plaintext []byte,
	_ string,
) (supportdomain.EncryptedImage, error) {
	ciphertext := append([]byte(nil), plaintext...)
	for index := range ciphertext {
		ciphertext[index] ^= 0x5a
	}
	return supportdomain.EncryptedImage{
		Ciphertext: ciphertext, Nonce: []byte("test-nonce12"),
		KeyVersion: "test-v1", Fingerprint: "test-fingerprint",
	}, nil
}

func (testImageCipher) Decrypt(
	_ context.Context,
	encrypted supportdomain.EncryptedImage,
	_ string,
) ([]byte, error) {
	plaintext := append([]byte(nil), encrypted.Ciphertext...)
	for index := range plaintext {
		plaintext[index] ^= 0x5a
	}
	return plaintext, nil
}

func (r *testRepository) CreateMessage(
	_ context.Context,
	message supportdomain.Message,
) (supportdomain.Message, bool, error) {
	r.messages = append(r.messages, message)
	return message, false, nil
}

func (r *testRepository) ListMessages(
	context.Context, string, int, string,
) ([]supportdomain.Message, error) {
	return r.messages, nil
}

func (r *testRepository) CountCustomerMessagesSince(
	context.Context, string, time.Time,
) (int, error) {
	return r.customerCount, nil
}

func (*testRepository) MarkAllRead(context.Context, string, time.Time) error { return nil }
func (*testRepository) CountUnread(context.Context, string) (int, error)     { return 2, nil }
func (r *testRepository) ListLatestByConversation(
	context.Context, bool, int,
) ([]supportdomain.ConversationHead, error) {
	heads := make([]supportdomain.ConversationHead, 0, len(r.messages))
	for _, message := range r.messages {
		heads = append(heads, supportdomain.ConversationHead{LastMessage: message})
	}
	return heads, nil
}
func (r *testRepository) GetAwaitingCustomerMessageID(context.Context, string) (string, error) {
	if len(r.messages) > 0 && r.messages[0].Author == supportdomain.AuthorCustomer &&
		r.handled.CustomerMessageID == "" {
		return r.messages[0].ID, nil
	}
	return "", nil
}
func (r *testRepository) MarkHandledWithoutReply(
	_ context.Context, _ string, resolution supportdomain.NoReplyResolution,
) (bool, error) {
	if len(r.messages) == 0 || r.messages[0].ID != resolution.CustomerMessageID ||
		r.messages[0].Author != supportdomain.AuthorCustomer {
		return false, supportdomain.ErrConversationNotAwaiting
	}
	if r.handled.CustomerMessageID != "" {
		return true, nil
	}
	r.handled = resolution
	return false, nil
}
func (*testRepository) CountAwaiting(context.Context) (int, error) { return 3, nil }

type testUsers struct{ known map[string]accountdomain.User }

func (u testUsers) ListUsersByID(
	_ context.Context,
	ids []accountdomain.UserID,
) ([]accountdomain.User, error) {
	users := make([]accountdomain.User, 0)
	for _, id := range ids {
		if user, ok := u.known[string(id)]; ok {
			users = append(users, user)
		}
	}
	return users, nil
}

type testOrdering struct{ owners map[string]string }

func (o testOrdering) ResolveOrderOwner(
	_ context.Context,
	agencyOrderID string,
) (string, error) {
	owner, ok := o.owners[agencyOrderID]
	if !ok {
		return "", errors.New("NOT_FOUND")
	}
	return owner, nil
}

func newTestHandler(repository *testRepository, ordering *testOrdering) *Handler {
	service := supportapp.NewService(
		repository,
		testUsers{known: map[string]accountdomain.User{
			"u1": {ID: "u1", Email: "u1@example.com", DisplayName: "고객"},
		}},
		testClock{}, testIDs{}, slog.New(slog.DiscardHandler),
	)
	if ordering != nil {
		service.EnableOrderingGateway(*ordering)
	}
	return NewHandler(service)
}

func newAttachmentTestHandler(repository *testAttachmentRepository) *Handler {
	service := supportapp.NewService(
		repository,
		testUsers{known: map[string]accountdomain.User{
			"u1": {ID: "u1", Email: "u1@example.com", DisplayName: "고객"},
			"u2": {ID: "u2", Email: "u2@example.com", DisplayName: "다른 고객"},
		}},
		testClock{}, testIDs{}, slog.New(slog.DiscardHandler),
	)
	service.EnableImageCipher(testImageCipher{})
	return NewHandler(service)
}

func TestSendMessageCreatesAndHidesInternalFields(t *testing.T) {
	repository := &testRepository{}
	handler := newTestHandler(repository, nil)
	request := httptest.NewRequest("POST", "/api/v1/support/messages", strings.NewReader(`{"body":"배송 문의드립니다"}`))
	request.Header.Set("Idempotency-Key", "0123456789abcdef")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "u1"))
	response := httptest.NewRecorder()
	handler.SendMessage(response, request)
	if response.Code != 201 || len(repository.messages) != 1 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "idempotencyKey") || strings.Contains(body, "userId") ||
		strings.Contains(body, "u1") {
		t.Fatalf("internal fields leaked: %s", body)
	}
	if !strings.Contains(body, "배송 문의드립니다") || !strings.Contains(body, "CUSTOMER") {
		t.Fatalf("body=%s", body)
	}
}

func TestSendMessageRequiresSession(t *testing.T) {
	handler := newTestHandler(&testRepository{}, nil)
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"body":"안녕하세요"}`))
	response := httptest.NewRecorder()
	handler.SendMessage(response, request)
	if response.Code != 401 {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestSendMessageRateLimit(t *testing.T) {
	handler := newTestHandler(&testRepository{customerCount: 30}, nil)
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"body":"안녕하세요"}`))
	request.Header.Set("Idempotency-Key", "0123456789abcdef")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "u1"))
	response := httptest.NewRecorder()
	handler.SendMessage(response, request)
	if response.Code != 429 || !strings.Contains(response.Body.String(), "SUPPORT_TOO_MANY_MESSAGES") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSendMessageMultipartImageAndPrivateDownloads(t *testing.T) {
	repository := &testAttachmentRepository{testRepository: &testRepository{}}
	handler := newAttachmentTestHandler(repository)
	pngBytes := testPNG(t)
	request := multipartMessageRequest(
		t, "/api/v1/support/messages", "사진을 확인해 주세요.", "",
		[]multipartImage{{name: "evidence.png", mediaType: "image/png", data: pngBytes}},
	)
	request.Header.Set("Idempotency-Key", "0123456789abcdef")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "u1"))
	response := httptest.NewRecorder()
	handler.SendMessage(response, request)
	if response.Code != http.StatusCreated || len(repository.messages) != 1 ||
		len(repository.messages[0].Attachments) != 1 {
		t.Fatalf("status=%d body=%s messages=%+v", response.Code, response.Body.String(), repository.messages)
	}
	metadata := repository.messages[0].Attachments[0]
	stored := repository.attachments[metadata.ID]
	if bytes.Equal(stored.Encrypted.Ciphertext, pngBytes) ||
		strings.Contains(response.Body.String(), "test-fingerprint") ||
		!strings.Contains(response.Body.String(), `"cacheControl":"private, no-store"`) {
		t.Fatalf("image evidence was not private: stored=%+v body=%s", stored, response.Body.String())
	}

	downloadRequest := httptest.NewRequest("GET", "/", nil)
	downloadRequest.SetPathValue("attachmentId", metadata.ID)
	downloadRequest = downloadRequest.WithContext(
		sharedapp.WithAuthenticatedUserID(downloadRequest.Context(), "u1"),
	)
	downloadResponse := httptest.NewRecorder()
	handler.DownloadImage(downloadResponse, downloadRequest)
	if downloadResponse.Code != http.StatusOK ||
		downloadResponse.Header().Get("Content-Type") != "image/png" ||
		downloadResponse.Header().Get("Cache-Control") != "private, no-store" ||
		downloadResponse.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(downloadResponse.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download status=%d headers=%v body=%s", downloadResponse.Code, downloadResponse.Header(), downloadResponse.Body.String())
	}
	if _, _, err := image.Decode(bytes.NewReader(downloadResponse.Body.Bytes())); err != nil {
		t.Fatalf("download image: %v", err)
	}

	foreignRequest := httptest.NewRequest("GET", "/", nil)
	foreignRequest.SetPathValue("attachmentId", metadata.ID)
	foreignRequest = foreignRequest.WithContext(
		sharedapp.WithAuthenticatedUserID(foreignRequest.Context(), "u2"),
	)
	foreignResponse := httptest.NewRecorder()
	handler.DownloadImage(foreignResponse, foreignRequest)
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign status=%d body=%s", foreignResponse.Code, foreignResponse.Body.String())
	}

	operatorRequest := httptest.NewRequest("GET", "/", nil)
	operatorRequest.SetPathValue("userId", "u1")
	operatorRequest.SetPathValue("attachmentId", metadata.ID)
	operatorRequest = operatorRequest.WithContext(
		sharedapp.WithAuthenticatedUserID(operatorRequest.Context(), "op1"),
	)
	operatorResponse := httptest.NewRecorder()
	handler.DownloadImageForOperator(operatorResponse, operatorRequest)
	if operatorResponse.Code != http.StatusOK ||
		operatorResponse.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("operator status=%d body=%s", operatorResponse.Code, operatorResponse.Body.String())
	}
}

func TestSendMessageMultipartRejectsFifthImage(t *testing.T) {
	repository := &testAttachmentRepository{testRepository: &testRepository{}}
	handler := newAttachmentTestHandler(repository)
	data := testPNG(t)
	images := make([]multipartImage, supportdomain.MaxImagesPerMessage+1)
	for index := range images {
		images[index] = multipartImage{
			name: "evidence.png", mediaType: "image/png", data: data,
		}
	}
	request := multipartMessageRequest(t, "/", "Too many", "", images)
	request.Header.Set("Idempotency-Key", "0123456789abcdef")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "u1"))
	response := httptest.NewRecorder()
	handler.SendMessage(response, request)
	if response.Code != http.StatusUnprocessableEntity || len(repository.messages) != 0 ||
		!strings.Contains(response.Body.String(), supportdomain.ErrImageAttachmentInvalid.Error()) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReplyOperatorAcceptsMultipartImage(t *testing.T) {
	repository := &testAttachmentRepository{testRepository: &testRepository{}}
	handler := newAttachmentTestHandler(repository)
	request := multipartMessageRequest(
		t, "/", "회수 사진을 확인했습니다.", "",
		[]multipartImage{{name: "review.png", mediaType: "image/png", data: testPNG(t)}},
	)
	request.SetPathValue("userId", "u1")
	request.Header.Set("Idempotency-Key", "fedcba9876543210")
	request = request.WithContext(
		sharedapp.WithAuthenticatedUserID(request.Context(), "op1"),
	)
	response := httptest.NewRecorder()
	handler.ReplyOperator(response, request)
	if response.Code != http.StatusCreated || len(repository.messages) != 1 ||
		repository.messages[0].Author != supportdomain.AuthorOperator ||
		len(repository.messages[0].Attachments) != 1 {
		t.Fatalf("status=%d body=%s messages=%+v", response.Code, response.Body.String(), repository.messages)
	}
}

func TestReplyOperatorRejectsForeignOrder(t *testing.T) {
	handler := newTestHandler(&testRepository{}, &testOrdering{owners: map[string]string{"o1": "u2"}})
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"body":"안내","agencyOrderId":"o1"}`))
	request.SetPathValue("userId", "u1")
	request.Header.Set("Idempotency-Key", "0123456789abcdef")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "op1"))
	response := httptest.NewRecorder()
	handler.ReplyOperator(response, request)
	if response.Code != 422 || !strings.Contains(response.Body.String(), "SUPPORT_ORDER_REF_INVALID") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSendOrderMessageAttachesOrder(t *testing.T) {
	repository := &testRepository{}
	handler := newTestHandler(repository, &testOrdering{owners: map[string]string{"o1": "u1"}})
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"body":"주문이 곧 발송됩니다"}`))
	request.SetPathValue("agencyOrderId", "o1")
	request.Header.Set("Idempotency-Key", "0123456789abcdef")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "op1"))
	response := httptest.NewRecorder()
	handler.SendOrderMessage(response, request)
	if response.Code != 201 || len(repository.messages) != 1 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	stored := repository.messages[0]
	if stored.UserID != "u1" || stored.AgencyOrderID != "o1" ||
		stored.Author != supportdomain.AuthorOperator {
		t.Fatalf("stored=%+v", stored)
	}
	if !strings.Contains(response.Body.String(), `"agencyOrderId":"o1"`) {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestSummaryReturnsUnread(t *testing.T) {
	handler := newTestHandler(&testRepository{}, nil)
	request := httptest.NewRequest("GET", "/api/v1/support/summary", nil)
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "u1"))
	response := httptest.NewRecorder()
	handler.Summary(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"unread":2`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestOperatorThreadIncludesAwaitingState(t *testing.T) {
	repository := &testRepository{messages: []supportdomain.Message{{
		ID:     "00000000-0000-4000-8000-000000000009",
		UserID: "u1", Author: supportdomain.AuthorCustomer,
		Body: "확인만 부탁드립니다.", CreatedAt: testClock{}.Now(),
	}}}
	handler := newTestHandler(repository, nil)
	request := httptest.NewRequest("GET", "/", nil)
	request.SetPathValue("userId", "u1")
	response := httptest.NewRecorder()
	handler.OperatorThread(response, request)
	if response.Code != 200 ||
		!strings.Contains(response.Body.String(), `"schemaVersion":"vitlane.support-thread.v2"`) ||
		!strings.Contains(response.Body.String(), `"awaitingReply":true`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCountsReturnsAwaiting(t *testing.T) {
	handler := newTestHandler(&testRepository{}, nil)
	response := httptest.NewRecorder()
	handler.Counts(response, httptest.NewRequest("GET", "/api/v1/admin/support/counts", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"awaiting":3`) ||
		!strings.Contains(response.Body.String(), `"schemaVersion":"vitlane.support-counts.v2"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHandleWithoutReplyMarksLatestCustomerMessage(t *testing.T) {
	repository := &testRepository{messages: []supportdomain.Message{{
		ID:     "00000000-0000-4000-8000-000000000009",
		UserID: "u1", Author: supportdomain.AuthorCustomer,
	}}}
	handler := newTestHandler(repository, nil)
	request := httptest.NewRequest("PUT", "/", nil)
	request.SetPathValue("userId", "u1")
	request.SetPathValue("messageId", repository.messages[0].ID)
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "op1"))
	response := httptest.NewRecorder()
	handler.HandleWithoutReply(response, request)
	if response.Code != 200 || repository.handled.CustomerMessageID != repository.messages[0].ID ||
		!strings.Contains(response.Body.String(), `"handled":true`) {
		t.Fatalf("status=%d body=%s handled=%+v", response.Code, response.Body.String(), repository.handled)
	}
}

func TestHandleWithoutReplyRejectsStaleMessage(t *testing.T) {
	repository := &testRepository{messages: []supportdomain.Message{{
		ID:     "00000000-0000-4000-8000-000000000009",
		UserID: "u1", Author: supportdomain.AuthorCustomer,
	}}}
	handler := newTestHandler(repository, nil)
	request := httptest.NewRequest("PUT", "/", nil)
	request.SetPathValue("userId", "u1")
	request.SetPathValue("messageId", "00000000-0000-4000-8000-000000000008")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "op1"))
	response := httptest.NewRecorder()
	handler.HandleWithoutReply(response, request)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "SUPPORT_CONVERSATION_NOT_AWAITING") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type multipartImage struct {
	name      string
	mediaType string
	data      []byte
}

func multipartMessageRequest(
	t *testing.T,
	target, body, agencyOrderID string,
	images []multipartImage,
) *http.Request {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	if err := writer.WriteField("body", body); err != nil {
		t.Fatal(err)
	}
	if agencyOrderID != "" {
		if err := writer.WriteField("agencyOrderId", agencyOrderID); err != nil {
			t.Fatal(err)
		}
	}
	for _, input := range images {
		header := make(textproto.MIMEHeader)
		header.Set(
			"Content-Disposition",
			`form-data; name="images"; filename="`+input.name+`"`,
		)
		header.Set("Content-Type", input.mediaType)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(input.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &payload)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 0x23, G: 0x76, B: 0xee, A: 0xff})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
