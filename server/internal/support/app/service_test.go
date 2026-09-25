package app_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	supportapp "github.com/vitlane/vitlane/server/internal/support/app"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

var now = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

const key = "0123456789abcdef"

type fakeClock struct{}

func (fakeClock) Now() time.Time { return now }

type fakeIDs struct{ next int }

func (f *fakeIDs) NewID() string { f.next++; return "id-" + string(rune('0'+f.next)) }

type fakeRepository struct {
	messages      []supportdomain.Message
	customerCount int
	markedRead    string
	unread        int
	awaiting      int
	handled       map[string]supportdomain.NoReplyResolution
}

func (f *fakeRepository) CreateMessage(
	_ context.Context,
	message supportdomain.Message,
) (supportdomain.Message, bool, error) {
	for _, existing := range f.messages {
		if existing.IdempotencyKey == message.IdempotencyKey {
			return existing, true, nil
		}
	}
	f.messages = append(f.messages, message)
	return message, false, nil
}

func (f *fakeRepository) ListMessages(
	_ context.Context, _ string, limit int, _ string,
) ([]supportdomain.Message, error) {
	if limit < len(f.messages) {
		return f.messages[:limit], nil
	}
	return f.messages, nil
}

func (f *fakeRepository) CountCustomerMessagesSince(
	_ context.Context, _ string, _ time.Time,
) (int, error) {
	return f.customerCount, nil
}

func (f *fakeRepository) MarkAllRead(_ context.Context, userID string, _ time.Time) error {
	f.markedRead = userID
	return nil
}

func (f *fakeRepository) CountUnread(_ context.Context, _ string) (int, error) {
	return f.unread, nil
}

func (f *fakeRepository) ListLatestByConversation(
	_ context.Context, awaitingOnly bool, _ int,
) ([]supportdomain.ConversationHead, error) {
	latest := make([]supportdomain.ConversationHead, 0)
	for _, message := range f.messages {
		head := supportdomain.ConversationHead{
			LastMessage:         message,
			HandledWithoutReply: f.handled != nil && f.handled[message.ID].CustomerMessageID != "",
		}
		if awaitingOnly && !head.AwaitingReply() {
			continue
		}
		latest = append(latest, head)
	}
	return latest, nil
}

func (f *fakeRepository) GetAwaitingCustomerMessageID(_ context.Context, _ string) (string, error) {
	if len(f.messages) == 0 {
		return "", nil
	}
	last := f.messages[0]
	if last.Author == supportdomain.AuthorCustomer &&
		(f.handled == nil || f.handled[last.ID].CustomerMessageID == "") {
		return last.ID, nil
	}
	return "", nil
}

func (f *fakeRepository) MarkHandledWithoutReply(
	_ context.Context,
	_ string,
	resolution supportdomain.NoReplyResolution,
) (bool, error) {
	if len(f.messages) == 0 || f.messages[0].ID != resolution.CustomerMessageID ||
		f.messages[0].Author != supportdomain.AuthorCustomer {
		return false, supportdomain.ErrConversationNotAwaiting
	}
	if f.handled == nil {
		f.handled = make(map[string]supportdomain.NoReplyResolution)
	}
	if _, found := f.handled[resolution.CustomerMessageID]; found {
		return true, nil
	}
	f.handled[resolution.CustomerMessageID] = resolution
	return false, nil
}

func (f *fakeRepository) CountAwaiting(context.Context) (int, error) {
	return f.awaiting, nil
}

type fakeUsers struct{ users map[string]accountdomain.User }

func (f fakeUsers) ListUsersByID(
	_ context.Context,
	ids []accountdomain.UserID,
) ([]accountdomain.User, error) {
	found := make([]accountdomain.User, 0)
	for _, id := range ids {
		if user, ok := f.users[string(id)]; ok {
			found = append(found, user)
		}
	}
	return found, nil
}

type fakeOrdering struct{ owners map[string]string }

func (f fakeOrdering) ResolveOrderOwner(
	_ context.Context,
	agencyOrderID string,
) (string, error) {
	owner, ok := f.owners[agencyOrderID]
	if !ok {
		return "", errors.New("NOT_FOUND")
	}
	return owner, nil
}

func newService(repository *fakeRepository, users fakeUsers) *supportapp.Service {
	return supportapp.NewService(
		repository, users, fakeClock{}, &fakeIDs{}, slog.New(slog.DiscardHandler),
	)
}

func TestSendCustomerMessageAndReplay(t *testing.T) {
	repository := &fakeRepository{}
	service := newService(repository, fakeUsers{})
	first, replay, err := service.SendCustomerMessage(
		context.Background(), "u1", "배송이 궁금해요", "", key,
	)
	if err != nil || replay {
		t.Fatalf("first=%+v replay=%t err=%v", first, replay, err)
	}
	second, replay, err := service.SendCustomerMessage(
		context.Background(), "u1", "배송이 궁금해요", "", key,
	)
	if err != nil || !replay || second.ID != first.ID {
		t.Fatalf("second=%+v replay=%t err=%v", second, replay, err)
	}
}

func TestSendCustomerMessageValidatesOwnOrderAttach(t *testing.T) {
	repository := &fakeRepository{}
	service := newService(repository, fakeUsers{})
	service.EnableOrderingGateway(fakeOrdering{owners: map[string]string{
		"o-mine": "u1", "o-other": "u2",
	}})
	message, _, err := service.SendCustomerMessage(
		context.Background(), "u1", "이 주문 관련 문의예요", "o-mine", key,
	)
	if err != nil || message.AgencyOrderID != "o-mine" {
		t.Fatalf("own order message=%+v err=%v", message, err)
	}
	if _, _, err := service.SendCustomerMessage(
		context.Background(), "u1", "남의 주문", "o-other", "0123456789abcdefff",
	); !errors.Is(err, supportdomain.ErrOrderRefInvalid) {
		t.Fatalf("foreign order err=%v", err)
	}
}

func TestSendCustomerMessageAttachWithoutGatewayRejected(t *testing.T) {
	service := newService(&fakeRepository{}, fakeUsers{})
	if _, _, err := service.SendCustomerMessage(
		context.Background(), "u1", "주문 문의", "o1", key,
	); !errors.Is(err, supportdomain.ErrOrderRefInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestSendCustomerMessageRateLimited(t *testing.T) {
	repository := &fakeRepository{customerCount: 30}
	service := newService(repository, fakeUsers{})
	_, _, err := service.SendCustomerMessage(context.Background(), "u1", "안녕하세요", "", key)
	if !errors.Is(err, supportdomain.ErrTooManyMessages) {
		t.Fatalf("err=%v", err)
	}
}

func TestReplyOperatorRejectsUnknownUser(t *testing.T) {
	service := newService(&fakeRepository{}, fakeUsers{})
	_, _, err := service.ReplyOperator(
		context.Background(), "ghost", "op1", "안내드립니다", "", key,
	)
	if !errors.Is(err, supportdomain.ErrUserNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestReplyOperatorValidatesOrderOwner(t *testing.T) {
	users := fakeUsers{users: map[string]accountdomain.User{
		"u1": {ID: "u1", Email: "u1@example.com"},
	}}
	service := newService(&fakeRepository{}, users)
	service.EnableOrderingGateway(fakeOrdering{owners: map[string]string{"o1": "u2"}})
	_, _, err := service.ReplyOperator(
		context.Background(), "u1", "op1", "주문 안내", "o1", key,
	)
	if !errors.Is(err, supportdomain.ErrOrderRefInvalid) {
		t.Fatalf("owner mismatch err=%v", err)
	}
	_, _, err = service.ReplyOperator(
		context.Background(), "u1", "op1", "주문 안내", "missing", key,
	)
	if !errors.Is(err, supportdomain.ErrOrderRefInvalid) {
		t.Fatalf("missing order err=%v", err)
	}
}

func TestReplyOperatorWithoutGatewayRejectsOrderRefOnly(t *testing.T) {
	users := fakeUsers{users: map[string]accountdomain.User{
		"u1": {ID: "u1", Email: "u1@example.com"},
	}}
	repository := &fakeRepository{}
	service := newService(repository, users)
	if _, _, err := service.ReplyOperator(
		context.Background(), "u1", "op1", "주문 안내", "o1", key,
	); !errors.Is(err, supportdomain.ErrOrderRefInvalid) {
		t.Fatalf("gateway nil + order ref err=%v", err)
	}
	message, _, err := service.ReplyOperator(
		context.Background(), "u1", "op1", "일반 안내", "", key,
	)
	if err != nil || message.Author != supportdomain.AuthorOperator {
		t.Fatalf("plain reply message=%+v err=%v", message, err)
	}
}

func TestSendOrderMessageResolvesOwner(t *testing.T) {
	repository := &fakeRepository{}
	service := newService(repository, fakeUsers{})
	service.EnableOrderingGateway(fakeOrdering{owners: map[string]string{"o1": "u9"}})
	message, replay, err := service.SendOrderMessage(
		context.Background(), "o1", "op1", "주문이 곧 발송됩니다", key,
	)
	if err != nil || replay {
		t.Fatalf("message=%+v replay=%t err=%v", message, replay, err)
	}
	if message.UserID != "u9" || message.AgencyOrderID != "o1" ||
		message.Author != supportdomain.AuthorOperator {
		t.Fatalf("message=%+v", message)
	}
}

func TestListConversationsEnrichesContactsAndAwaiting(t *testing.T) {
	repository := &fakeRepository{}
	users := fakeUsers{users: map[string]accountdomain.User{
		"u1": {ID: "u1", Email: "u1@example.com", DisplayName: "고객"},
	}}
	service := newService(repository, users)
	if _, _, err := service.SendCustomerMessage(
		context.Background(), "u1", "문의합니다", "", key,
	); err != nil {
		t.Fatal(err)
	}
	conversations, err := service.ListConversations(context.Background(), "AWAITING", 0)
	if err != nil || len(conversations) != 1 {
		t.Fatalf("conversations=%+v err=%v", conversations, err)
	}
	conversation := conversations[0]
	if !conversation.AwaitingReply || conversation.AwaitingCustomerMessageID == "" || conversation.Customer == nil ||
		conversation.Customer.Email != "u1@example.com" {
		t.Fatalf("conversation=%+v", conversation)
	}
	if _, err := service.ListConversations(context.Background(), "BOGUS", 0); !errors.Is(err, supportdomain.ErrQueryInvalid) {
		t.Fatalf("view err=%v", err)
	}
}

func TestOperatorThreadRequiresKnownUser(t *testing.T) {
	service := newService(&fakeRepository{}, fakeUsers{})
	if _, err := service.OperatorThread(context.Background(), "ghost", 0, ""); !errors.Is(err, supportdomain.ErrUserNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestMarkAllRead(t *testing.T) {
	repository := &fakeRepository{}
	service := newService(repository, fakeUsers{})
	if err := service.MarkAllRead(context.Background(), " u1 "); err != nil {
		t.Fatal(err)
	}
	if repository.markedRead != "u1" {
		t.Fatalf("markedRead=%q", repository.markedRead)
	}
}

func TestHandleWithoutReplyIsIdempotentAndLeavesNewCustomerMessageAwaiting(t *testing.T) {
	users := fakeUsers{users: map[string]accountdomain.User{
		"u1": {ID: "u1", Email: "u1@example.com"},
	}}
	repository := &fakeRepository{messages: []supportdomain.Message{{
		ID: "m1", UserID: "u1", Author: supportdomain.AuthorCustomer,
	}}}
	service := newService(repository, users)

	replay, err := service.HandleWithoutReply(context.Background(), "u1", "m1", "op1")
	if err != nil || replay {
		t.Fatalf("first replay=%t err=%v", replay, err)
	}
	replay, err = service.HandleWithoutReply(context.Background(), "u1", "m1", "op1")
	if err != nil || !replay {
		t.Fatalf("second replay=%t err=%v", replay, err)
	}

	repository.messages = []supportdomain.Message{{
		ID: "m2", UserID: "u1", Author: supportdomain.AuthorCustomer,
	}}
	conversations, err := service.ListConversations(context.Background(), "AWAITING", 10)
	if err != nil || len(conversations) != 1 || !conversations[0].AwaitingReply {
		t.Fatalf("conversations=%+v err=%v", conversations, err)
	}
	if _, err := service.HandleWithoutReply(context.Background(), "u1", "m1", "op1"); !errors.Is(err, supportdomain.ErrConversationNotAwaiting) {
		t.Fatalf("stale message err=%v", err)
	}
}
