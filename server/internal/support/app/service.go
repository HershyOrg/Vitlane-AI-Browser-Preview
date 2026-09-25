// Package app은 Support 대화(ADR-0059)의 유스케이스다. 고객은 자기 대화에
// 쓰고 읽고, 운영자는 대화 목록·스레드를 읽고 답한다. 교차 제품 의존은
// app 포트(OrderingGateway·UserDirectory)로만 받는다 — wiring이 owner app
// service를 주입한다(operator 제품 전례).
package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

// customerHourlyLimit — 인증 사용자 발신 폭주 가드(마케팅 문의 전례의 완화판).
const customerHourlyLimit = 30

type Repository interface {
	CreateMessage(context.Context, supportdomain.Message) (supportdomain.Message, bool, error)
	// ListMessages는 최신순(DESC)이다. beforeID가 있으면 그 메시지보다 오래된
	// 페이지를 돌려준다.
	ListMessages(ctx context.Context, userID string, limit int, beforeID string) ([]supportdomain.Message, error)
	CountCustomerMessagesSince(ctx context.Context, userID string, since time.Time) (int, error)
	// MarkAllRead는 고객 읽음 워터마크다 — 미읽음 OPERATOR 메시지 전체를 한 번에.
	MarkAllRead(ctx context.Context, userID string, now time.Time) error
	CountUnread(ctx context.Context, userID string) (int, error)
	// ListLatestByConversation은 화면용 마지막 메시지와 별도의 최신 ordinary
	// TEXT 처리 여부를 함께 돌린다(최근 대화 순). awaitingOnly면 아직 운영
	// 처리가 필요한 대화만 남는다.
	ListLatestByConversation(ctx context.Context, awaitingOnly bool, limit int) ([]supportdomain.ConversationHead, error)
	GetAwaitingCustomerMessageID(ctx context.Context, userID string) (string, error)
	MarkHandledWithoutReply(
		ctx context.Context,
		userID string,
		resolution supportdomain.NoReplyResolution,
	) (bool, error)
	CountAwaiting(ctx context.Context) (int, error)
}

type AttachmentRepository interface {
	CreateMessageWithAttachments(
		context.Context,
		supportdomain.Message,
		[]supportdomain.ImageAttachment,
	) (supportdomain.Message, bool, error)
	GetImageAttachment(
		ctx context.Context,
		attachmentID string,
		userID string,
	) (supportdomain.ImageAttachment, error)
}

type BusinessRepository interface {
	AttachmentRepository
	ListLatestByConversationView(
		ctx context.Context,
		view supportdomain.ConversationView,
		limit int,
	) ([]supportdomain.ConversationHead, error)
	HasActionRequired(ctx context.Context, userID string) (bool, error)
	CountActionRequired(ctx context.Context) (int, error)
	FindOpenActionCard(
		ctx context.Context,
		userID string,
		referenceType string,
		referenceID string,
		idempotencyKey string,
	) (string, error)
}

// ImageCipher is intentionally a narrow port. Runtime wiring may adapt the
// existing versioned PII keyring; Support never has a plaintext persistence
// fallback when the keyring is unavailable.
type ImageCipher interface {
	Encrypt(
		context.Context,
		[]byte,
		string,
	) (supportdomain.EncryptedImage, error)
	Decrypt(
		context.Context,
		supportdomain.EncryptedImage,
		string,
	) ([]byte, error)
}

// OrderingGateway는 주문 첨부의 소유자 검증 포트다. agencyorder의
// LifecycleService가 구조적으로 충족한다(wiring 주입, ADR-0059 §5).
type OrderingGateway interface {
	ResolveOrderOwner(ctx context.Context, agencyOrderID string) (string, error)
}

// UserDirectory는 운영자 화면의 고객 표시용 최소 신원 포트다. account의
// Service가 충족한다 — 다른 제품이 users 테이블을 직접 읽지 않는다.
type UserDirectory interface {
	ListUsersByID(ctx context.Context, ids []accountdomain.UserID) ([]accountdomain.User, error)
}

type CustomerContact struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

type Conversation struct {
	UserID string `json:"userId"`
	// Customer가 nil이면 계정 행 자체가 없는 경우다. 탈퇴(tombstone) 계정은
	// 빈 email로 온다 — 표시는 Web이 정한다.
	Customer                  *CustomerContact      `json:"customer,omitempty"`
	LastMessage               supportdomain.Message `json:"lastMessage"`
	AwaitingReply             bool                  `json:"awaitingReply"`
	AwaitingCustomerMessageID string                `json:"awaitingCustomerMessageId,omitempty"`
	ActionRequired            bool                  `json:"actionRequired"`
}

type Thread struct {
	UserID                    string                  `json:"userId"`
	Customer                  *CustomerContact        `json:"customer,omitempty"`
	Messages                  []supportdomain.Message `json:"messages"`
	AwaitingReply             bool                    `json:"awaitingReply"`
	AwaitingCustomerMessageID string                  `json:"awaitingCustomerMessageId,omitempty"`
	ActionRequired            bool                    `json:"actionRequired"`
}

type Service struct {
	repository  Repository
	users       UserDirectory
	ordering    OrderingGateway
	clock       sharedapp.Clock
	ids         sharedapp.IDGenerator
	logger      *slog.Logger
	imageCipher ImageCipher
}

func NewService(
	repository Repository,
	users UserDirectory,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
	logger *slog.Logger,
) *Service {
	return &Service{
		repository: repository, users: users, clock: clock, ids: ids, logger: logger,
	}
}

// EnableOrderingGateway — agencyOrder 런타임이 있을 때만 배선된다. nil이면
// 주문 첨부 발신만 거절되고 순수 대화는 그대로 동작한다.
func (s *Service) EnableOrderingGateway(gateway OrderingGateway) {
	s.ordering = gateway
}

func (s *Service) EnableImageCipher(cipher ImageCipher) {
	s.imageCipher = cipher
}

// SendCustomerMessage — 주문 첨부는 운영자와 대칭이다: 고객은 자기 소유
// 주문만 첨부할 수 있고 소유권은 게이트웨이로 검증한다(ADR-0059 §5).
func (s *Service) SendCustomerMessage(
	ctx context.Context,
	userID, body, agencyOrderID, idempotencyKey string,
) (supportdomain.Message, bool, error) {
	return s.sendCustomerMessage(
		ctx, userID, body, agencyOrderID, idempotencyKey, nil,
	)
}

func (s *Service) SendCustomerMessageWithImages(
	ctx context.Context,
	userID, body, agencyOrderID, idempotencyKey string,
	images []ImageInput,
) (supportdomain.Message, bool, error) {
	return s.sendCustomerMessage(
		ctx, userID, body, agencyOrderID, idempotencyKey, images,
	)
}

func (s *Service) sendCustomerMessage(
	ctx context.Context,
	userID, body, agencyOrderID, idempotencyKey string,
	images []ImageInput,
) (supportdomain.Message, bool, error) {
	userID = strings.TrimSpace(userID)
	agencyOrderID = strings.TrimSpace(agencyOrderID)
	if agencyOrderID != "" {
		if err := s.verifyOrderOwner(ctx, agencyOrderID, userID); err != nil {
			return supportdomain.Message{}, false, err
		}
	}
	now := s.clock.Now()
	message, err := supportdomain.NewCustomerMessage(
		s.ids.NewID(), userID, body, agencyOrderID, idempotencyKey, now,
	)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	count, err := s.repository.CountCustomerMessagesSince(
		ctx, message.UserID, now.Add(-time.Hour),
	)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	if count >= customerHourlyLimit {
		return supportdomain.Message{}, false, supportdomain.ErrTooManyMessages
	}
	created, replay, err := s.createMessage(ctx, message, images, now)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	// 본문은 절대 로그에 싣지 않는다(ADR-0059 §5).
	s.logger.InfoContext(ctx, "support message sent",
		"event", "support.message.sent", "message_id", created.ID,
		"author", created.Author, "order_attached", created.AgencyOrderID != "",
		"image_count", len(created.Attachments),
		"replay", replay,
	)
	return created, replay, nil
}

func (s *Service) ListMessages(
	ctx context.Context,
	userID string,
	limit int,
	beforeID string,
) ([]supportdomain.Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repository.ListMessages(
		ctx, strings.TrimSpace(userID), limit, strings.TrimSpace(beforeID),
	)
}

func (s *Service) MarkAllRead(ctx context.Context, userID string) error {
	return s.repository.MarkAllRead(ctx, strings.TrimSpace(userID), s.clock.Now())
}

func (s *Service) CountUnread(ctx context.Context, userID string) (int, error) {
	return s.repository.CountUnread(ctx, strings.TrimSpace(userID))
}

func (s *Service) ListConversations(
	ctx context.Context,
	view string,
	limit int,
) ([]Conversation, error) {
	conversationView, err := supportdomain.ParseConversationView(view)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var heads []supportdomain.ConversationHead
	if business, ok := s.repository.(BusinessRepository); ok {
		heads, err = business.ListLatestByConversationView(ctx, conversationView, limit)
	} else if conversationView == supportdomain.ConversationViewActionRequired {
		return nil, supportdomain.ErrQueryInvalid
	} else {
		heads, err = s.repository.ListLatestByConversation(
			ctx, conversationView == supportdomain.ConversationViewAwaitingReply, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	latest := make([]supportdomain.Message, 0, len(heads))
	for _, head := range heads {
		latest = append(latest, head.LastMessage)
	}
	contacts, err := s.resolveContacts(ctx, latest)
	if err != nil {
		return nil, err
	}
	conversations := make([]Conversation, 0, len(latest))
	for _, head := range heads {
		message := head.LastMessage
		conversations = append(conversations, Conversation{
			UserID:                    message.UserID,
			Customer:                  contacts[message.UserID],
			LastMessage:               message,
			AwaitingReply:             head.AwaitingReply(),
			AwaitingCustomerMessageID: head.AwaitingCustomerMessageID(),
			ActionRequired:            head.ActionRequired,
		})
	}
	return conversations, nil
}

func (s *Service) OperatorThread(
	ctx context.Context,
	userID string,
	limit int,
	beforeID string,
) (Thread, error) {
	userID = strings.TrimSpace(userID)
	contact, found, err := s.resolveContact(ctx, userID)
	if err != nil {
		return Thread{}, err
	}
	if !found {
		return Thread{}, supportdomain.ErrUserNotFound
	}
	messages, err := s.ListMessages(ctx, userID, limit, beforeID)
	if err != nil {
		return Thread{}, err
	}
	awaitingCustomerMessageID, err := s.repository.GetAwaitingCustomerMessageID(ctx, userID)
	if err != nil {
		return Thread{}, err
	}
	actionRequired := false
	if business, ok := s.repository.(BusinessRepository); ok {
		actionRequired, err = business.HasActionRequired(ctx, userID)
		if err != nil {
			return Thread{}, err
		}
	}
	return Thread{
		UserID: userID, Customer: contact, Messages: messages,
		AwaitingReply:             awaitingCustomerMessageID != "",
		AwaitingCustomerMessageID: awaitingCustomerMessageID,
		ActionRequired:            actionRequired,
	}, nil
}

// HandleWithoutReply는 마지막 고객 메시지를 특정해 운영 완료로 처리한다.
// 메시지 원문·author를 바꾸거나 고객에게 메시지를 발송하지 않는다. 같은
// 메시지에 대한 재시도는 멱등이며, 새 고객 메시지는 다시 대기 상태가 된다.
func (s *Service) HandleWithoutReply(
	ctx context.Context,
	userID, customerMessageID, operatorUserID string,
) (bool, error) {
	userID = strings.TrimSpace(userID)
	if _, found, err := s.resolveContact(ctx, userID); err != nil {
		return false, err
	} else if !found {
		return false, supportdomain.ErrUserNotFound
	}
	resolution, err := supportdomain.NewNoReplyResolution(
		customerMessageID, operatorUserID, s.clock.Now(),
	)
	if err != nil {
		return false, err
	}
	replay, err := s.repository.MarkHandledWithoutReply(ctx, userID, resolution)
	if err != nil {
		return false, err
	}
	s.logger.InfoContext(ctx, "support conversation handled without reply",
		"event", "support.conversation.handled_without_reply",
		"customer_message_id", resolution.CustomerMessageID,
		"operator_id", resolution.HandledByUserID,
		"replay", replay,
	)
	return replay, nil
}

func (s *Service) ReplyOperator(
	ctx context.Context,
	targetUserID, operatorUserID, body, agencyOrderID, idempotencyKey string,
) (supportdomain.Message, bool, error) {
	return s.replyOperator(
		ctx, targetUserID, operatorUserID, body, agencyOrderID, idempotencyKey, nil,
	)
}

func (s *Service) ReplyOperatorWithImages(
	ctx context.Context,
	targetUserID, operatorUserID, body, agencyOrderID, idempotencyKey string,
	images []ImageInput,
) (supportdomain.Message, bool, error) {
	return s.replyOperator(
		ctx, targetUserID, operatorUserID, body, agencyOrderID, idempotencyKey, images,
	)
}

func (s *Service) replyOperator(
	ctx context.Context,
	targetUserID, operatorUserID, body, agencyOrderID, idempotencyKey string,
	images []ImageInput,
) (supportdomain.Message, bool, error) {
	targetUserID = strings.TrimSpace(targetUserID)
	if _, found, err := s.resolveContact(ctx, targetUserID); err != nil {
		return supportdomain.Message{}, false, err
	} else if !found {
		return supportdomain.Message{}, false, supportdomain.ErrUserNotFound
	}
	agencyOrderID = strings.TrimSpace(agencyOrderID)
	if agencyOrderID != "" {
		if err := s.verifyOrderOwner(ctx, agencyOrderID, targetUserID); err != nil {
			return supportdomain.Message{}, false, err
		}
	}
	return s.publishOperatorWithImages(
		ctx, targetUserID, operatorUserID, body, agencyOrderID, idempotencyKey,
		images,
	)
}

// SendOrderMessage는 종전 "고객 안내 보내기"의 대체 창구다 — 주문으로 대상
// 대화(소유 고객)를 정하고 주문 참조를 첨부해 발신한다(ADR-0059 §3).
func (s *Service) SendOrderMessage(
	ctx context.Context,
	agencyOrderID, operatorUserID, body, idempotencyKey string,
) (supportdomain.Message, bool, error) {
	agencyOrderID = strings.TrimSpace(agencyOrderID)
	if s.ordering == nil || agencyOrderID == "" {
		return supportdomain.Message{}, false, supportdomain.ErrOrderRefInvalid
	}
	owner, err := s.ordering.ResolveOrderOwner(ctx, agencyOrderID)
	if err != nil {
		return supportdomain.Message{}, false, orderRefError(err)
	}
	return s.publishOperator(
		ctx, owner, operatorUserID, body, agencyOrderID, idempotencyKey,
	)
}

func (s *Service) SendOrderMessageWithImages(
	ctx context.Context,
	agencyOrderID, operatorUserID, body, idempotencyKey string,
	images []ImageInput,
) (supportdomain.Message, bool, error) {
	agencyOrderID = strings.TrimSpace(agencyOrderID)
	if s.ordering == nil || agencyOrderID == "" {
		return supportdomain.Message{}, false, supportdomain.ErrOrderRefInvalid
	}
	owner, err := s.ordering.ResolveOrderOwner(ctx, agencyOrderID)
	if err != nil {
		return supportdomain.Message{}, false, orderRefError(err)
	}
	return s.publishOperatorWithImages(
		ctx, owner, operatorUserID, body, agencyOrderID, idempotencyKey, images,
	)
}

func (s *Service) CountAwaiting(ctx context.Context) (int, error) {
	return s.repository.CountAwaiting(ctx)
}

func (s *Service) CountActionRequired(ctx context.Context) (int, error) {
	business, ok := s.repository.(BusinessRepository)
	if !ok {
		// Older adapters cannot contain typed business cards, so their action
		// count is deterministically zero. This keeps the ordinary-text Support
		// adapter source-compatible while the richer repository is wired.
		return 0, nil
	}
	return business.CountActionRequired(ctx)
}

func (s *Service) publishOperator(
	ctx context.Context,
	userID, operatorUserID, body, agencyOrderID, idempotencyKey string,
) (supportdomain.Message, bool, error) {
	return s.publishOperatorWithImages(
		ctx, userID, operatorUserID, body, agencyOrderID, idempotencyKey, nil,
	)
}

func (s *Service) publishOperatorWithImages(
	ctx context.Context,
	userID, operatorUserID, body, agencyOrderID, idempotencyKey string,
	images []ImageInput,
) (supportdomain.Message, bool, error) {
	now := s.clock.Now()
	message, err := supportdomain.NewOperatorMessage(
		s.ids.NewID(), userID, operatorUserID, body,
		agencyOrderID, idempotencyKey, now,
	)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	created, replay, err := s.createMessage(ctx, message, images, now)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	s.logger.InfoContext(ctx, "support message sent",
		"event", "support.message.sent", "message_id", created.ID,
		"author", created.Author, "order_attached", created.AgencyOrderID != "",
		"image_count", len(created.Attachments),
		"replay", replay,
	)
	return created, replay, nil
}

func (s *Service) createMessage(
	ctx context.Context,
	message supportdomain.Message,
	images []ImageInput,
	now time.Time,
) (supportdomain.Message, bool, error) {
	if len(images) == 0 {
		return s.repository.CreateMessage(ctx, message)
	}
	preparedAttachments, err := s.prepareImages(
		ctx, message.ID, message.UserID, images, now,
	)
	if err != nil {
		return supportdomain.Message{}, false, err
	}
	hashes := make([]string, 0, len(preparedAttachments))
	for _, attachment := range preparedAttachments {
		hashes = append(hashes, attachment.ContentSHA256)
	}
	message.RequestHash = supportdomain.CanonicalRequestHash(message, hashes)
	attachmentRepository, ok := s.repository.(AttachmentRepository)
	if !ok {
		return supportdomain.Message{}, false, supportdomain.ErrImageAttachmentInvalid
	}
	return attachmentRepository.CreateMessageWithAttachments(
		ctx, message, preparedAttachments,
	)
}

func (s *Service) verifyOrderOwner(
	ctx context.Context,
	agencyOrderID, targetUserID string,
) error {
	if s.ordering == nil {
		return supportdomain.ErrOrderRefInvalid
	}
	owner, err := s.ordering.ResolveOrderOwner(ctx, agencyOrderID)
	if err != nil {
		return orderRefError(err)
	}
	if owner != targetUserID {
		return supportdomain.ErrOrderRefInvalid
	}
	return nil
}

// orderRefError — 인프라 분류 오류(fault)는 그대로 올리고, 미존재 같은 업무
// 오류는 주문 참조 오류 하나로 접는다(존재 여부 탐침을 넓히지 않는다).
func orderRefError(err error) error {
	if _, classified := fault.As(err); classified {
		return err
	}
	return supportdomain.ErrOrderRefInvalid
}

func (s *Service) resolveContact(
	ctx context.Context,
	userID string,
) (*CustomerContact, bool, error) {
	if userID == "" {
		return nil, false, nil
	}
	users, err := s.users.ListUsersByID(
		ctx, []accountdomain.UserID{accountdomain.UserID(userID)},
	)
	if err != nil {
		return nil, false, err
	}
	if len(users) == 0 {
		return nil, false, nil
	}
	return &CustomerContact{
		Email: users[0].Email, DisplayName: users[0].DisplayName,
	}, true, nil
}

func (s *Service) resolveContacts(
	ctx context.Context,
	messages []supportdomain.Message,
) (map[string]*CustomerContact, error) {
	ids := make([]accountdomain.UserID, 0, len(messages))
	seen := make(map[string]bool, len(messages))
	for _, message := range messages {
		if !seen[message.UserID] {
			seen[message.UserID] = true
			ids = append(ids, accountdomain.UserID(message.UserID))
		}
	}
	contacts := make(map[string]*CustomerContact, len(ids))
	if len(ids) == 0 {
		return contacts, nil
	}
	users, err := s.users.ListUsersByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, user := range users {
		contacts[string(user.ID)] = &CustomerContact{
			Email: user.Email, DisplayName: user.DisplayName,
		}
	}
	return contacts, nil
}
