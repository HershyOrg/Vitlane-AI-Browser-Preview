// Package app exposes the only runtime mutation boundary for Live admission.
package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/livecontrol/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type StaticPolicy struct {
	OrderIssue     bool `json:"orderIssue"`
	PayPalMoney    bool `json:"paypalMoney"`
	MerchantEffect bool `json:"merchantEffect"`
}

func (p StaticPolicy) Allows(scope domain.Scope) bool {
	switch scope {
	case domain.ScopeOrderIssue:
		return p.OrderIssue
	case domain.ScopePayPalMoney:
		return p.PayPalMoney
	case domain.ScopeMerchantEffect:
		return p.MerchantEffect
	case domain.ScopeAll:
		return p.OrderIssue && p.PayPalMoney && p.MerchantEffect
	default:
		return false
	}
}

type Repository interface {
	Get(context.Context) (domain.State, error)
	LockForAdmission(context.Context) (domain.State, error)
	Apply(context.Context, Change) (domain.State, error)
	RecordRejected(context.Context, Attempt) error
}

type Change struct {
	AuditID         string
	Scope           domain.Scope
	Kill            bool
	Actor           string
	Reason          string
	ExpectedVersion int64
	ChangedAt       time.Time
}

// ChangeTime is kept separate from Change's transport-neutral fields so the
// repository can use a concrete time without accepting untrusted timestamps.
type ChangeRequest struct {
	Scope           string
	Confirmation    string
	Reason          string
	ExpectedVersion int64
	Actor           string
}

type Attempt struct {
	AuditID         string
	Action          string
	Scope           string
	Actor           string
	Reason          string
	ExpectedVersion int64
	ReasonCode      string
	AttemptedAt     time.Time
}

type Gate struct {
	StaticAllowed bool `json:"staticAllowed"`
	RuntimeKilled bool `json:"runtimeKilled"`
	Effective     bool `json:"effective"`
}

type Snapshot struct {
	Version        int64  `json:"version"`
	OrderIssue     Gate   `json:"orderIssue"`
	PayPalMoney    Gate   `json:"paypalMoney"`
	MerchantEffect Gate   `json:"merchantEffect"`
	ChangedAt      string `json:"changedAt"`
	ChangedBy      string `json:"changedBy"`
	Reason         string `json:"reason"`
}

type Service struct {
	repository Repository
	static     StaticPolicy
	clock      sharedapp.Clock
	ids        sharedapp.IDGenerator
}

func NewService(repository Repository, static StaticPolicy, clock sharedapp.Clock, ids sharedapp.IDGenerator) *Service {
	return &Service{repository: repository, static: static, clock: clock, ids: ids}
}

func (s *Service) State(ctx context.Context) (Snapshot, error) {
	state, err := s.repository.Get(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return s.project(state), nil
}

func (s *Service) Allows(ctx context.Context, scope domain.Scope) (bool, int64, error) {
	state, err := s.repository.Get(ctx)
	if err != nil {
		return false, 0, err
	}
	return s.static.Allows(scope) && !state.Killed(scope), state.Version, nil
}

func (s *Service) Kill(ctx context.Context, input ChangeRequest) (Snapshot, error) {
	return s.change(ctx, input, true)
}

func (s *Service) Reactivate(ctx context.Context, input ChangeRequest) (Snapshot, error) {
	return s.change(ctx, input, false)
}

func (s *Service) change(ctx context.Context, input ChangeRequest, kill bool) (Snapshot, error) {
	auditID := s.ids.NewID()
	scope, err := domain.ParseScope(input.Scope)
	reason := strings.TrimSpace(input.Reason)
	actor := strings.TrimSpace(input.Actor)
	action := "KILL"
	if !kill {
		action = "REACTIVATE"
	}
	if err != nil || actor == "" || len(reason) < 8 || input.ExpectedVersion < 1 ||
		(!kill && !s.static.Allows(scope)) ||
		input.Confirmation != domain.Confirmation(scope, kill) {
		if auditErr := s.repository.RecordRejected(ctx, Attempt{
			AuditID: auditID, Action: action, Scope: strings.TrimSpace(input.Scope),
			Actor: actor, Reason: reason, ExpectedVersion: input.ExpectedVersion,
			ReasonCode:  "LIVE_CONTROL_CONFIRMATION_INVALID",
			AttemptedAt: s.clock.Now(),
		}); auditErr != nil {
			return Snapshot{}, auditErr
		}
		return Snapshot{}, domain.ErrInvalid
	}
	state, err := s.repository.Apply(ctx, Change{
		AuditID: auditID, Scope: scope, Kill: kill, Actor: actor,
		Reason: reason, ExpectedVersion: input.ExpectedVersion,
		ChangedAt: s.clock.Now(),
	})
	if err != nil {
		if errors.Is(err, domain.ErrVersionConflict) {
			return Snapshot{}, err
		}
		return Snapshot{}, err
	}
	return s.project(state), nil
}

func (s *Service) project(state domain.State) Snapshot {
	gate := func(scope domain.Scope) Gate {
		static := s.static.Allows(scope)
		killed := state.Killed(scope)
		return Gate{StaticAllowed: static, RuntimeKilled: killed, Effective: static && !killed}
	}
	return Snapshot{
		Version: state.Version, OrderIssue: gate(domain.ScopeOrderIssue),
		PayPalMoney: gate(domain.ScopePayPalMoney), MerchantEffect: gate(domain.ScopeMerchantEffect),
		ChangedAt: state.ChangedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		ChangedBy: state.ChangedBy, Reason: state.Reason,
	}
}

func (s *Service) AllowLiveOrderIssue(ctx context.Context) (bool, int64, error) {
	return s.Allows(ctx, domain.ScopeOrderIssue)
}

// LockLiveOrderIssueAdmission must be called inside the transaction that
// persists the AgencyOrder. The shared row lock linearizes a successful kill
// with the final issue commit: either the order commits before kill returns or
// it observes the killed state and fails closed.
func (s *Service) LockLiveOrderIssueAdmission(ctx context.Context) (bool, int64, error) {
	state, err := s.repository.LockForAdmission(ctx)
	if err != nil {
		return false, 0, err
	}
	return s.static.Allows(domain.ScopeOrderIssue) && !state.Killed(domain.ScopeOrderIssue),
		state.Version, nil
}

func (s *Service) AllowLiveMoneyEffect(ctx context.Context) (bool, error) {
	allowed, _, err := s.Allows(ctx, domain.ScopePayPalMoney)
	return allowed, err
}

func (s *Service) AllowLiveMerchantEffect(ctx context.Context) (bool, error) {
	allowed, _, err := s.Allows(ctx, domain.ScopeMerchantEffect)
	return allowed, err
}
