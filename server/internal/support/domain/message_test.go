package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

var now = time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)

const key = "0123456789abcdef"

func TestNewCustomerMessageTrimsAndValidates(t *testing.T) {
	message, err := supportdomain.NewCustomerMessage("m1", "u1", "  도와주세요  ", "", key, now)
	if err != nil {
		t.Fatal(err)
	}
	if message.Body != "도와주세요" || message.Author != supportdomain.AuthorCustomer ||
		message.AgencyOrderID != "" || message.CreatedAt != now {
		t.Fatalf("message=%+v", message)
	}
}

func TestNewCustomerMessageBodyBounds(t *testing.T) {
	if _, err := supportdomain.NewCustomerMessage("m1", "u1", "   ", "", key, now); !errors.Is(err, supportdomain.ErrMessageInvalid) {
		t.Fatalf("blank body err=%v", err)
	}
	longest := strings.Repeat("가", 2000)
	if _, err := supportdomain.NewCustomerMessage("m1", "u1", longest, "", key, now); err != nil {
		t.Fatalf("2000 runes err=%v", err)
	}
	if _, err := supportdomain.NewCustomerMessage("m1", "u1", longest+"가", "", key, now); !errors.Is(err, supportdomain.ErrMessageInvalid) {
		t.Fatalf("2001 runes err=%v", err)
	}
}

func TestNewMessageRequiresIdempotencyKey(t *testing.T) {
	if _, err := supportdomain.NewCustomerMessage("m1", "u1", "안녕하세요", "", "short", now); !errors.Is(err, supportdomain.ErrIdempotencyKeyMissing) {
		t.Fatalf("short key err=%v", err)
	}
	if _, err := supportdomain.NewCustomerMessage("m1", "u1", "안녕하세요", "", strings.Repeat("k", 121), now); !errors.Is(err, supportdomain.ErrIdempotencyKeyMissing) {
		t.Fatalf("long key err=%v", err)
	}
}

func TestNewCustomerMessageCarriesOrderRef(t *testing.T) {
	// 주문 첨부는 운영자와 대칭이다 — 소유권 검증은 app 계층이 강제한다.
	message, err := supportdomain.NewCustomerMessage(
		"m1", "u1", "이 주문 관련 문의예요", "order-1", key, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if message.AgencyOrderID != "order-1" || message.Author != supportdomain.AuthorCustomer {
		t.Fatalf("message=%+v", message)
	}
}

func TestNewOperatorMessageCarriesOrderRef(t *testing.T) {
	message, err := supportdomain.NewOperatorMessage(
		"m1", "u1", "op1", "주문이 곧 발송됩니다.", "order-1", key, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if message.Author != supportdomain.AuthorOperator ||
		message.AgencyOrderID != "order-1" || message.CreatedByUserID != "op1" {
		t.Fatalf("message=%+v", message)
	}
}

func TestNewOperatorMessageRequiresOperator(t *testing.T) {
	if _, err := supportdomain.NewOperatorMessage("m1", "u1", " ", "안내", "", key, now); !errors.Is(err, supportdomain.ErrMessageInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestConversationHeadAwaitingReply(t *testing.T) {
	customer := supportdomain.ConversationHead{LastMessage: supportdomain.Message{
		Author: supportdomain.AuthorCustomer,
	}}
	if !customer.AwaitingReply() {
		t.Fatal("unhandled customer message must await reply")
	}
	customer.HandledWithoutReply = true
	if customer.AwaitingReply() {
		t.Fatal("handled customer message must leave awaiting queue")
	}
	operator := supportdomain.ConversationHead{LastMessage: supportdomain.Message{
		Author: supportdomain.AuthorOperator,
	}}
	if operator.AwaitingReply() {
		t.Fatal("operator message must not await reply")
	}
}

func TestConversationHeadExposesExactOrdinaryCustomerMessageBehindBusinessCard(t *testing.T) {
	head := supportdomain.ConversationHead{
		LastMessage:           supportdomain.Message{ID: "card-1", Author: supportdomain.AuthorSystem, ContentKind: supportdomain.ContentKindBusinessCard},
		LastOrdinaryMessageID: "customer-text-1",
		LastOrdinaryAuthor:    supportdomain.AuthorCustomer,
	}
	if got := head.AwaitingCustomerMessageID(); got != "customer-text-1" {
		t.Fatalf("awaiting message id=%q", got)
	}
	head.HandledWithoutReply = true
	if got := head.AwaitingCustomerMessageID(); got != "" {
		t.Fatalf("handled conversation exposed target=%q", got)
	}
}

func TestNewNoReplyResolutionRequiresMessageAndOperator(t *testing.T) {
	resolution, err := supportdomain.NewNoReplyResolution(" m1 ", " op1 ", now)
	if err != nil || resolution.CustomerMessageID != "m1" ||
		resolution.HandledByUserID != "op1" {
		t.Fatalf("resolution=%+v err=%v", resolution, err)
	}
	if _, err := supportdomain.NewNoReplyResolution("", "op1", now); !errors.Is(err, supportdomain.ErrConversationNotAwaiting) {
		t.Fatalf("missing message err=%v", err)
	}
}
