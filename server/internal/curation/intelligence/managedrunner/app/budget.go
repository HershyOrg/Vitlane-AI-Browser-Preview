package app

import (
	"context"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"

	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
)

// reservationTTL outlives the current five-minute attempt deadline so its
// finalizer can settle first. Expiry is only the threshold for marking a
// crashed call UNKNOWN; it is not evidence that budget can be returned.
const reservationTTL = 10 * time.Minute

// BudgetService enforces the ADR-0032 daily caps. Reservation happens before
// the model call and settlement after, so two concurrent calls can never both
// pass a check that only one of them fits under.
type BudgetService struct {
	ledger     LedgerPort
	limits     runnerdomain.Limits
	transactor sharedapp.Transactor
	clock      sharedapp.Clock
	ids        sharedapp.IDGenerator
}

func NewBudgetService(
	ledger LedgerPort,
	limits runnerdomain.Limits,
	transactor sharedapp.Transactor,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
) *BudgetService {
	return &BudgetService{
		ledger: ledger, limits: limits, transactor: transactor,
		clock: clock, ids: ids,
	}
}

func (s *BudgetService) Limits() runnerdomain.Limits {
	return s.limits
}

// Reserve takes the worst-case cost of one model call under row locks. It
// returns ErrServerDailyLimit or ErrUserDailyLimit without calling the model,
// so exceeding a cap costs nothing.
func (s *BudgetService) Reserve(
	ctx context.Context,
	userID string,
	attemptID string,
	model runnerdomain.Model,
	estimatedInputTokens int64,
) (runnerdomain.Reservation, error) {
	return s.ReserveCall(
		ctx, userID, attemptID, "budget:"+s.ids.NewID(), model,
		estimatedInputTokens,
	)
}

// ReserveCall binds the budget row to the stable external request key. Tests
// and non-provider callers may use Reserve, which creates an internal key.
func (s *BudgetService) ReserveCall(
	ctx context.Context,
	userID string,
	attemptID string,
	requestKey string,
	model runnerdomain.Model,
	estimatedInputTokens int64,
) (runnerdomain.Reservation, error) {
	now := s.clock.Now()
	usageDate := runnerdomain.UsageDate(now)
	worstCost := model.WorstCost(estimatedInputTokens)
	if worstCost <= 0 {
		// A zero-priced model would make the cap unenforceable, so treat it as
		// a misconfiguration rather than a free call.
		worstCost = 1
	}

	reservation := runnerdomain.Reservation{
		ID:           s.ids.NewID(),
		UsageDate:    usageDate,
		UserID:       userID,
		AttemptID:    attemptID,
		RequestKey:   requestKey,
		AmountMicros: worstCost,
		Status:       runnerdomain.ReservationHeld,
		ExpiresAt:    now.Add(reservationTTL),
		CreatedAt:    now,
	}
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		server, user, err := s.ledger.LockCounters(
			txContext, usageDate, userID, now,
		)
		if err != nil {
			return err
		}
		limits := s.limits
		if userID == "" {
			limits.UserDaily = limits.ServerAdmission
		}
		if err := limits.Admit(server, user, worstCost); err != nil {
			return err
		}
		if err := s.ledger.InsertReservation(txContext, reservation); err != nil {
			return err
		}
		return s.ledger.AddReserved(txContext, usageDate, userID, worstCost, now)
	})
	if err != nil {
		return runnerdomain.Reservation{}, err
	}
	return reservation, nil
}

// MarkUnknown closes a reservation without returning its headroom. It is the
// conservative terminal state when a paid call may have happened but usage
// could not be durably settled.
func (s *BudgetService) MarkUnknown(
	ctx context.Context,
	reservation runnerdomain.Reservation,
) error {
	now := s.clock.Now()
	return s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		_, err := s.ledger.CloseReservation(
			txContext, reservation.ID, runnerdomain.ReservationUnknown, now,
		)
		return err
	})
}

// Settle replaces a HELD reservation with the real cost. If another terminal
// transition already won, the actual spend is added without returning held
// headroom. UNKNOWN reconciliation is a separate audited operation (GAP-024).
func (s *BudgetService) Settle(
	ctx context.Context,
	reservation runnerdomain.Reservation,
	model runnerdomain.Model,
	usage runnerdomain.TokenUsage,
) error {
	now := s.clock.Now()
	actual := model.Cost(usage)
	return s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		closed, err := s.ledger.CloseReservation(
			txContext, reservation.ID, runnerdomain.ReservationSettled, now,
		)
		if err != nil {
			return err
		}
		if _, _, err := s.ledger.LockCounters(
			txContext, reservation.UsageDate, reservation.UserID, now,
		); err != nil {
			return err
		}
		release := reservation.AmountMicros
		if !closed {
			release = 0
		}
		return s.ledger.Settle(
			txContext, reservation.UsageDate, reservation.UserID,
			release, actual, usage, now,
		)
	})
}

// Release returns a reservation whose call never happened, for example when the
// model errored before producing usage.
func (s *BudgetService) Release(
	ctx context.Context,
	reservation runnerdomain.Reservation,
) error {
	now := s.clock.Now()
	return s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		closed, err := s.ledger.CloseReservation(
			txContext, reservation.ID, runnerdomain.ReservationReleased, now,
		)
		if err != nil || !closed {
			return err
		}
		if _, _, err := s.ledger.LockCounters(
			txContext, reservation.UsageDate, reservation.UserID, now,
		); err != nil {
			return err
		}
		return s.ledger.Settle(
			txContext, reservation.UsageDate, reservation.UserID,
			reservation.AmountMicros, 0, runnerdomain.TokenUsage{}, now,
		)
	})
}

// UnknownResolutionInput is the audited operator decision for one UNKNOWN
// reservation. EvidenceReference points at the provider billing/usage
// evidence matched against the reservation's stable request key.
type UnknownResolutionInput struct {
	ReservationID       string
	OperatorUserID      string
	Outcome             runnerdomain.ReservationStatus
	ReasonDetail        string
	EvidenceReference   string
	SettledAmountMicros *int64
}

// ResolveUnknown closes an UNKNOWN reservation exactly once (GAP-024,
// ADR-0040 §9). RELEASED returns the held headroom because provider evidence
// shows the call never billed; SETTLED converts the headroom into settled
// spend — at the evidenced amount, or conservatively at the full held amount
// when the provider does not publish an exact figure. There is no automatic
// path into this method.
func (s *BudgetService) ResolveUnknown(
	ctx context.Context,
	input UnknownResolutionInput,
) (runnerdomain.Reservation, error) {
	if input.Outcome != runnerdomain.ReservationSettled &&
		input.Outcome != runnerdomain.ReservationReleased {
		return runnerdomain.Reservation{}, runnerdomain.ErrReservationNotUnknown
	}
	now := s.clock.Now()
	var resolved runnerdomain.Reservation
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		reservation, found, err := s.ledger.ReservationForUpdate(
			txContext, input.ReservationID,
		)
		if err != nil {
			return err
		}
		if !found {
			return runnerdomain.ErrReservationNotFound
		}
		if reservation.Status != runnerdomain.ReservationUnknown {
			return runnerdomain.ErrReservationNotUnknown
		}
		changed, err := s.ledger.ResolveUnknownReservation(
			txContext, reservation.ID, input.Outcome, now,
		)
		if err != nil {
			return err
		}
		if !changed {
			return runnerdomain.ErrReservationNotUnknown
		}
		if _, _, err := s.ledger.LockCounters(
			txContext, reservation.UsageDate, reservation.UserID, now,
		); err != nil {
			return err
		}
		settledMicros := int64(0)
		if input.Outcome == runnerdomain.ReservationSettled {
			settledMicros = reservation.AmountMicros
			if input.SettledAmountMicros != nil {
				settledMicros = *input.SettledAmountMicros
			}
		}
		if err := s.ledger.Settle(
			txContext, reservation.UsageDate, reservation.UserID,
			reservation.AmountMicros, settledMicros,
			runnerdomain.TokenUsage{}, now,
		); err != nil {
			return err
		}
		if err := s.ledger.InsertReservationResolution(
			txContext, runnerdomain.ReservationResolution{
				ReservationID:       reservation.ID,
				OperatorUserID:      input.OperatorUserID,
				Outcome:             input.Outcome,
				ReasonDetail:        input.ReasonDetail,
				EvidenceReference:   input.EvidenceReference,
				SettledAmountMicros: input.SettledAmountMicros,
				CreatedAt:           now,
			},
		); err != nil {
			return err
		}
		reservation.Status = input.Outcome
		reservation.CompletedAt = &now
		resolved = reservation
		return nil
	})
	if err != nil {
		return runnerdomain.Reservation{}, err
	}
	return resolved, nil
}

// MarkExpiredUnknown is the crash sweep. Expiry proves only that the caller is
// gone; it does not prove a paid request was never executed.
func (s *BudgetService) MarkExpiredUnknown(ctx context.Context, limit int) (int, error) {
	now := s.clock.Now()
	expired, err := s.ledger.ExpiredReservations(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	marked := 0
	for _, reservation := range expired {
		if err := s.MarkUnknown(ctx, reservation); err != nil {
			return marked, err
		}
		marked++
	}
	return marked, nil
}

// Usage is the read model behind the account panel and the pre-submit gate.
type Usage struct {
	UsageDate           time.Time
	UserSpentMicros     int64
	UserLimitMicros     int64
	ServerSpentMicros   int64
	ServerLimitMicros   int64
	UserExhausted       bool
	ServerExhausted     bool
	ServerAdmissionMicr int64
}

func (s *BudgetService) Usage(
	ctx context.Context,
	userID string,
) (Usage, error) {
	now := s.clock.Now()
	usageDate := runnerdomain.UsageDate(now)
	server, user, err := s.ledger.ReadCounters(ctx, usageDate, userID)
	if err != nil {
		return Usage{}, err
	}
	return Usage{
		UsageDate:           usageDate,
		UserSpentMicros:     user.CommittedMicros(),
		UserLimitMicros:     s.limits.UserDaily,
		ServerSpentMicros:   server.CommittedMicros(),
		ServerLimitMicros:   s.limits.ServerHard,
		ServerAdmissionMicr: s.limits.ServerAdmission,
		UserExhausted:       user.CommittedMicros() >= s.limits.UserDaily,
		ServerExhausted:     s.limits.ServerExhausted(server),
	}, nil
}

type ServerUsageDay struct {
	UsageDate time.Time
	Counters  runnerdomain.UsageCounters
}

type ServerUsageSeries struct {
	From                 time.Time
	Through              time.Time
	DailyLimitMicros     int64
	AdmissionLimitMicros int64
	Days                 []ServerUsageDay
	Totals               runnerdomain.UsageCounters
}

// ServerUsageHistory returns a continuous UTC series for the operator panel.
// Missing ledger rows become explicit zero days so the graph never implies
// that two non-adjacent dates were consecutive.
func (s *BudgetService) ServerUsageHistory(
	ctx context.Context,
	days int,
) (ServerUsageSeries, error) {
	if days <= 0 {
		days = 30
	}
	through := runnerdomain.UsageDate(s.clock.Now())
	from := through.AddDate(0, 0, -(days - 1))
	recorded, err := s.ledger.ListServerUsage(ctx, from, through)
	if err != nil {
		return ServerUsageSeries{}, err
	}
	byDate := make(map[string]runnerdomain.UsageCounters, len(recorded))
	for _, day := range recorded {
		byDate[day.UsageDate.UTC().Format("2006-01-02")] = day.Counters
	}
	series := ServerUsageSeries{
		From:                 from,
		Through:              through,
		DailyLimitMicros:     s.limits.ServerHard,
		AdmissionLimitMicros: s.limits.ServerAdmission,
		Days:                 make([]ServerUsageDay, 0, days),
	}
	for offset := 0; offset < days; offset++ {
		date := from.AddDate(0, 0, offset)
		counters := byDate[date.Format("2006-01-02")]
		series.Days = append(series.Days, ServerUsageDay{
			UsageDate: date,
			Counters:  counters,
		})
		series.Totals.ReservedMicros += counters.ReservedMicros
		series.Totals.SettledMicros += counters.SettledMicros
		series.Totals.RequestCount += counters.RequestCount
		series.Totals.InputTokens += counters.InputTokens
		series.Totals.OutputTokens += counters.OutputTokens
	}
	return series, nil
}
