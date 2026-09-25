package app

import (
	"context"
	"errors"
	"strings"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

var ErrOperatorActionReasonInvalid = errors.New("OPERATOR_ACTION_REASON_INVALID")

// ActiveSessionView is the operator-facing session row. It exists so an
// operator can find whose access to cut; it never carries tokens or hashes.
type ActiveSessionView struct {
	SessionID string
	UserID    string
	Email     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// OperatorActionAudit is the append-only record of a sensitive operator
// action (ADR-0040 §10).
type OperatorActionAudit struct {
	ID             string
	OperatorUserID string
	Action         string
	SubjectUserID  string
	ReasonDetail   string
	CreatedAt      time.Time
}

type OperatorSessionRepository interface {
	ListActiveSessions(
		ctx context.Context, now time.Time, limit int,
	) ([]ActiveSessionView, error)
	RevokeUserSessions(
		ctx context.Context, userID string, now time.Time,
	) (int64, error)
	InsertOperatorActionAudit(
		ctx context.Context, audit OperatorActionAudit,
	) error
}

// OperatorSessionService backs the ops screen: list active sessions and cut
// a user's access with an audited, reasoned decision.
type OperatorSessionService struct {
	repository OperatorSessionRepository
	transactor sharedapp.Transactor
	clock      sharedapp.Clock
	ids        sharedapp.IDGenerator
}

func NewOperatorSessionService(
	repository OperatorSessionRepository,
	transactor sharedapp.Transactor,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
) *OperatorSessionService {
	return &OperatorSessionService{
		repository: repository, transactor: transactor, clock: clock, ids: ids,
	}
}

const activeSessionListLimit = 500

func (s *OperatorSessionService) ListActiveSessions(
	ctx context.Context,
) ([]ActiveSessionView, error) {
	return s.repository.ListActiveSessions(
		ctx, s.clock.Now(), activeSessionListLimit,
	)
}

// RevokeUserSessions revokes every active session of one user and records
// the decision in the same transaction. Revoking a user with no active
// sessions still records the audit: the operator's decision happened.
func (s *OperatorSessionService) RevokeUserSessions(
	ctx context.Context,
	operatorUserID string,
	subjectUserID string,
	reasonDetail string,
) (int64, error) {
	reason := strings.TrimSpace(reasonDetail)
	if length := len([]rune(reason)); length < 8 || length > 500 {
		return 0, ErrOperatorActionReasonInvalid
	}
	now := s.clock.Now()
	var revoked int64
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		count, err := s.repository.RevokeUserSessions(txContext, subjectUserID, now)
		if err != nil {
			return err
		}
		revoked = count
		return s.repository.InsertOperatorActionAudit(txContext, OperatorActionAudit{
			ID:             s.ids.NewID(),
			OperatorUserID: operatorUserID,
			Action:         "SESSION_REVOKE_ALL",
			SubjectUserID:  subjectUserID,
			ReasonDetail:   reason,
			CreatedAt:      now,
		})
	})
	if err != nil {
		return 0, err
	}
	return revoked, nil
}
