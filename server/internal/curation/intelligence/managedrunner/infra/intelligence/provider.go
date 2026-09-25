// Package intelligence adapts the Managed runner into the common
// IntelligenceProvider port.
//
// ADR-0038 puts cost admission entirely on this side of the seam: the workflow
// hands over a completion request and gets back JSON or a shared fault code. It
// never sees a token count, a reservation or a price, so no product code can
// come to depend on Managed-specific accounting.
package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

type Provider struct {
	budget    *runnerapp.BudgetService
	model     runnerapp.ModelPort
	registry  runnerdomain.Registry
	finalizer runtimepolicy.Finalizer
}

func NewProvider(
	budget *runnerapp.BudgetService,
	model runnerapp.ModelPort,
	registry runnerdomain.Registry,
	finalizer runtimepolicy.Finalizer,
) (*Provider, error) {
	if budget == nil || model == nil {
		return nil, runnerdomain.ErrDisabled
	}
	return &Provider{
		budget: budget, model: model, registry: registry, finalizer: finalizer,
	}, nil
}

func (p *Provider) Kind() intelligencedomain.ProviderKind {
	return intelligencedomain.ProviderManaged
}

// Available answers before a job is dispatched, so an exhausted server budget
// leaves work pending instead of burning an attempt on a call that cannot run.
func (p *Provider) Available(ctx context.Context, userID string) error {
	usage, err := p.budget.Usage(ctx, userID)
	if err != nil {
		// A ledger read failure is not evidence that the budget is spent, so
		// the caller is told the provider is momentarily unavailable rather
		// than that the user is out of allowance.
		return fault.Wrap(
			err, fault.ProviderUnavailable,
			intelligencedomain.ReasonProviderUnavailable, true,
		)
	}
	if usage.ServerExhausted {
		return fault.New(
			fault.QuotaExceeded, intelligencedomain.ReasonQuotaExceeded, false,
		)
	}
	// With a user named, the answer covers the user's own allowance too: a
	// round that cannot afford its query and one evaluation batch is refused
	// here, before it spends catalog calls it would have to abandon.
	if userID != "" {
		model, err := p.registry.Lookup("")
		if err == nil && usage.UserSpentMicros+model.MinimumResearchHeadroom() > usage.UserLimitMicros {
			return fault.New(
				fault.QuotaExceeded, intelligencedomain.ReasonQuotaExceeded, false,
			)
		}
	}
	return nil
}

// Complete reserves budget, runs one model call, then settles the real usage.
// Only a failure known to precede an external effect releases the reservation;
// ambiguous calls retain headroom as UNKNOWN.
func (p *Provider) Complete(
	ctx context.Context,
	request intelligenceapp.CompletionRequest,
) (intelligenceapp.CompletionResult, error) {
	model, err := p.registry.Lookup(request.ModelKey)
	if err != nil {
		return intelligenceapp.CompletionResult{}, fault.Wrap(
			err, fault.InvalidInput,
			intelligencedomain.ReasonProviderUnavailable, false,
		)
	}
	if !request.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = runtimepolicy.WithDeadline(ctx, request.Deadline)
		defer cancel()
	}

	if request.SchemaName == intelligenceapp.SchemaCandidateRanking && model.AssessmentOutputTokens > 0 {
		candidates, axes := assessmentSchemaSize(request.Schema)
		model = model.AssessmentModel(candidates, axes)
	}
	images := []runnerapp.ModelImage{}
	if model.SupportsLowImages && model.LowImageInputTokens > 0 && request.SchemaName == intelligenceapp.SchemaCandidateRanking {
		for _, i := range request.Images {
			images = append(images, runnerapp.ModelImage{ObservationID: i.ObservationID, URL: i.URL})
		}
	}
	schemaJSON, _ := json.Marshal(request.Schema)
	// UTF-8 bytes conservatively bound text tokens, including Korean and the
	// strict schema. Image tokens have their own verified model allowance.
	estimatedInput := int64(len(request.SystemPrompt)+len(request.UserPrompt)+len(schemaJSON)+128) + int64(len(images))*model.LowImageInputTokens

	reservation, err := p.budget.ReserveCall(
		ctx, request.UserID, request.AttemptID, request.RequestKey,
		model, estimatedInput,
	)
	if err != nil {
		return intelligenceapp.CompletionResult{}, mapBudgetError(err)
	}
	response, callErr := p.model.Complete(ctx, runnerapp.ModelRequest{
		Images: images, Model: model,
		RequestKey:   request.RequestKey,
		SystemPrompt: request.SystemPrompt,
		UserPrompt:   request.UserPrompt,
		SchemaName:   request.SchemaName,
		Schema:       request.Schema,
	})
	used := response.Usage.InputTokens > 0 || response.Usage.OutputTokens > 0
	finalizeContext, cancelFinalize := p.finalizer.Context()
	defer cancelFinalize()
	if used {
		if err := p.budget.Settle(finalizeContext, reservation, model, response.Usage); err != nil {
			p.markUnknown(reservation)
			return intelligenceapp.CompletionResult{}, fault.Wrap(
				err, fault.ExternalEffectUnknown,
				intelligencedomain.ReasonEffectUnknown, false,
			)
		}
	} else if callErr == nil {
		p.markUnknown(reservation)
		return intelligenceapp.CompletionResult{}, fault.New(
			fault.ExternalEffectUnknown,
			intelligencedomain.ReasonEffectUnknown, false,
		)
	} else if failure, classified := fault.As(callErr); classified &&
		failure.Code == fault.ExternalEffectUnknown {
		if err := p.budget.MarkUnknown(finalizeContext, reservation); err != nil {
			return intelligenceapp.CompletionResult{}, fault.Wrap(
				err, fault.ExternalEffectUnknown,
				intelligencedomain.ReasonEffectUnknown, false,
			)
		}
	} else if err := p.budget.Release(finalizeContext, reservation); err != nil {
		return intelligenceapp.CompletionResult{}, fault.Wrap(
			err, fault.InternalFailure,
			intelligencedomain.ReasonInternalFailure, false,
		)
	}
	if callErr != nil && len(images) > 0 {
		if f, ok := fault.As(callErr); ok && f.Reason == "MANAGED_IMAGE_REJECTED" {
			request.Images = nil
			request.SystemPrompt += "\nImages are unavailable for this evaluation. Do not cite image fact IDs or claim to have observed images. Use only provided text; mark unsupported visual evidence UNKNOWN.\n"
			request.RequestKey += ":text-fallback"
			return p.Complete(ctx, request)
		}
	}
	if callErr != nil {
		if retryable := retryableAfterUnknownCall(ctx, callErr); retryable != nil {
			return intelligenceapp.CompletionResult{}, retryable
		}
		return intelligenceapp.CompletionResult{}, mapModelError(ctx, callErr)
	}
	return intelligenceapp.CompletionResult{Content: response.Content}, nil
}

// retryableAfterUnknownCall reclassifies a model call that timed out or hit a
// provider 5xx after the request was written. The only external effect of a
// model call is its cost, and the reservation above has already been kept as
// UNKNOWN for that; the product state is untouched because the answer never
// arrived. Repeating the call is therefore safe for the attempt, and parking
// the job would leave the curation blocked until someone cancelled it. A
// caller cancellation stays EFFECT_UNKNOWN: the job is being closed anyway.
func retryableAfterUnknownCall(ctx context.Context, err error) error {
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.ExternalEffectUnknown {
		return nil
	}
	if failure.Reason == "MANAGED_MODEL_HTTP_5XX_UNKNOWN" {
		return fault.Wrap(
			err, fault.ProviderUnavailable,
			intelligencedomain.ReasonProviderUnavailable, true,
		)
	}
	if isTimeout(err) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fault.Wrap(
			err, fault.DeadlineExceeded,
			intelligencedomain.ReasonDeadlineExceeded, true,
		)
	}
	return nil
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// Sweep marks expired reservations unknown. A crash does not prove the paid
// provider request was never executed, so releasing its headroom is unsafe.
func (p *Provider) Sweep(ctx context.Context, _ time.Time) error {
	_, err := p.budget.MarkExpiredUnknown(ctx, 50)
	return err
}

func (p *Provider) markUnknown(reservation runnerdomain.Reservation) {
	ctx, cancel := p.finalizer.Context()
	defer cancel()
	_ = p.budget.MarkUnknown(ctx, reservation)
}

func mapBudgetError(err error) error {
	switch {
	case errors.Is(err, runnerdomain.ErrUserDailyLimit),
		errors.Is(err, runnerdomain.ErrServerDailyLimit):
		// A cap is final until it resets, so retrying now would only spend
		// attempts on a call that cannot succeed.
		return fault.Wrap(
			err, fault.QuotaExceeded,
			intelligencedomain.ReasonQuotaExceeded, false,
		)
	case errors.Is(err, runnerdomain.ErrReservationExists):
		// The provider does not promise idempotency for our correlation key.
		// A duplicate reservation therefore blocks a second paid call until
		// provider-side reconciliation exists.
		return fault.Wrap(
			err, fault.ExternalEffectUnknown,
			intelligencedomain.ReasonEffectUnknown, false,
		)
	default:
		return fault.Wrap(
			err, fault.InternalFailure,
			intelligencedomain.ReasonInternalFailure, false,
		)
	}
}

func mapModelError(ctx context.Context, err error) error {
	if classified, ok := fault.As(err); ok {
		return classified
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fault.Wrap(
			err, fault.DeadlineExceeded,
			intelligencedomain.ReasonDeadlineExceeded, true,
		)
	case errors.Is(err, context.Canceled) && ctx.Err() != nil:
		return fault.Wrap(
			err, fault.CallerCancelled,
			intelligencedomain.ReasonInternalFailure, false,
		)
	case errors.Is(err, runnerdomain.ErrModelResponse):
		// The model answered but not in the requested shape. Another call
		// usually resolves it, and the bound on attempts stops that from
		// becoming an open-ended spend.
		return fault.Wrap(
			err, fault.ProviderRejected,
			intelligencedomain.ReasonProviderResponse, true,
		)
	default:
		return fault.Wrap(
			err, fault.ProviderUnavailable,
			intelligencedomain.ReasonProviderUnavailable, true,
		)
	}
}

func assessmentSchemaSize(schema map[string]any) (int, int) {
	properties, _ := schema["properties"].(map[string]any)
	ranked, _ := properties["ranked"].(map[string]any)
	candidates, _ := ranked["maxItems"].(int)
	item, _ := ranked["items"].(map[string]any)
	fields, _ := item["properties"].(map[string]any)
	scores, _ := fields["axisScores"].(map[string]any)
	axes, _ := scores["maxItems"].(int)
	return candidates, axes
}
