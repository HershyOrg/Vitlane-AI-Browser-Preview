// Package domain owns the durable state and safety rules for an on-device
// merchant browser run. It deliberately stops before final payment: a run can
// prepare a checkout review, but only the user may enter credentials, OTP or
// payment details and submit the merchant's final action.
package domain

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalid             = errors.New("browser run input is invalid")
	ErrInvalidTransition   = errors.New("browser run state transition is invalid")
	ErrRunNotFound         = errors.New("browser run not found")
	ErrCandidateNotFound   = errors.New("browser run candidate not found")
	ErrVersionConflict     = errors.New("browser run version conflict")
	ErrIdempotencyConflict = errors.New("browser run idempotency conflict")
	digestPattern          = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	currencyPattern        = regexp.MustCompile(`^[A-Z]{3}$`)
)

type State string

const (
	StateAwaitingNavigationApproval  State = "AWAITING_NAVIGATION_APPROVAL"
	StateNavigationApproved          State = "NAVIGATION_APPROVED"
	StateAwaitingPreparationApproval State = "AWAITING_PREPARATION_APPROVAL"
	StatePreparationApproved         State = "PREPARATION_APPROVED"
	StateUserControl                 State = "USER_CONTROL"
	StateResumeRequiresObservation   State = "RESUME_REQUIRES_OBSERVATION"
	StatePaused                      State = "PAUSED"
	StateReadyForUserPayment         State = "READY_FOR_USER_PAYMENT"
	StateCompleted                   State = "COMPLETED"
	StatePartial                     State = "PARTIAL"
	StateFailed                      State = "FAILED"
	StateResultUnknown               State = "RESULT_UNKNOWN"
	StateCancelled                   State = "CANCELLED"
)

type ControlOwner string

const (
	ControlNone  ControlOwner = "NONE"
	ControlAgent ControlOwner = "AGENT"
	ControlUser  ControlOwner = "USER"
)

type PreparationStep string

const (
	StepSelectVariant      PreparationStep = "SELECT_VARIANT"
	StepSetQuantity        PreparationStep = "SET_QUANTITY"
	StepAddToCart          PreparationStep = "ADD_TO_CART"
	StepOpenCheckoutReview PreparationStep = "OPEN_CHECKOUT_REVIEW"
)

type HandoffReason string

const (
	HandoffSignInRequired         HandoffReason = "SIGN_IN_REQUIRED"
	HandoffVerificationRequired   HandoffReason = "VERIFICATION_REQUIRED"
	HandoffUnsupportedInteraction HandoffReason = "UNSUPPORTED_INTERACTION"
	HandoffUserRequested          HandoffReason = "USER_REQUESTED"
	HandoffFinalPaymentRequired   HandoffReason = "FINAL_PAYMENT_REQUIRED"
)

type ObservationKind string

const (
	ObservationPublicProduct  ObservationKind = "PUBLIC_PRODUCT"
	ObservationSignInRequired ObservationKind = "SIGN_IN_REQUIRED"
	ObservationCheckoutReview ObservationKind = "CHECKOUT_REVIEW"
	ObservationResult         ObservationKind = "RESULT"
)

// SessionStateHint is privacy-safe, page-derived metadata. It is untrusted and
// never proves authentication or expands an approval. In particular, unknown
// may resume public browsing without being presented as signed in.
type SessionStateHint string

const (
	SessionAuthenticated SessionStateHint = "authenticated"
	SessionAnonymous     SessionStateHint = "anonymous"
	SessionUnknown       SessionStateHint = "unknown"
)

type ResultEvidenceSource string

const (
	EvidenceMerchantObserved ResultEvidenceSource = "MERCHANT_OBSERVED"
	EvidenceUserReported     ResultEvidenceSource = "USER_REPORTED"
)

type ResultOutcome string

const (
	OutcomeSucceeded ResultOutcome = "SUCCEEDED"
	OutcomePartial   ResultOutcome = "PARTIAL"
	OutcomeFailed    ResultOutcome = "FAILED"
	OutcomeUnknown   ResultOutcome = "UNKNOWN"
)

type CandidateReference struct {
	UserID         string
	CurationID     string
	CandidateID    string
	ProductURL     string
	MerchantOrigin string
	MerchantHost   string
}

func NewCandidateReference(userID, curationID, candidateID, rawURL string) (CandidateReference, error) {
	canonical, origin, host, err := CanonicalMerchantURL(rawURL)
	if err != nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(curationID) == "" ||
		strings.TrimSpace(candidateID) == "" {
		return CandidateReference{}, fmt.Errorf("%w: candidate reference", ErrInvalid)
	}
	return CandidateReference{
		UserID: strings.TrimSpace(userID), CurationID: strings.TrimSpace(curationID),
		CandidateID: strings.TrimSpace(candidateID), ProductURL: canonical,
		MerchantOrigin: origin, MerchantHost: host,
	}, nil
}

type PreparationApproval struct {
	PlanRevision            int64             `json:"planRevision"`
	QuoteDigest             string            `json:"quoteDigest"`
	AllowedPreparationSteps []PreparationStep `json:"allowedPreparationSteps"`
	PriceCeilingMinor       int64             `json:"priceCeilingMinor"`
	PriceCurrency           string            `json:"priceCurrency"`
	ApprovedAt              time.Time         `json:"approvedAt"`
}

type Observation struct {
	Revision           int64            `json:"revision"`
	Kind               ObservationKind  `json:"kind"`
	Origin             string           `json:"origin"`
	PageIdentityDigest string           `json:"pageIdentityDigest"`
	SessionStateHint   SessionStateHint `json:"sessionStateHint,omitempty"`
	ObservedAt         time.Time        `json:"observedAt"`
}

type ResultVerification struct {
	Outcome             ResultOutcome        `json:"outcome"`
	EvidenceSource      ResultEvidenceSource `json:"evidenceSource"`
	EvidenceDigest      string               `json:"evidenceDigest,omitempty"`
	ObservationRevision int64                `json:"observationRevision,omitempty"`
	VerifiedAt          time.Time            `json:"verifiedAt"`
}

type Run struct {
	ID             string `json:"id"`
	UserID         string `json:"-"`
	CurationID     string `json:"curationId"`
	CandidateID    string `json:"candidateId"`
	ProductURL     string `json:"productUrl"`
	MerchantOrigin string `json:"merchantOrigin"`
	MerchantHost   string `json:"merchantHost"`

	State        State        `json:"state"`
	ControlOwner ControlOwner `json:"controlOwner"`
	Version      int64        `json:"version"`

	NavigationApprovedAt     *time.Time           `json:"navigationApprovedAt,omitempty"`
	Preparation              *PreparationApproval `json:"preparationApproval,omitempty"`
	LatestObservation        *Observation         `json:"latestObservation,omitempty"`
	HandoffReason            HandoffReason        `json:"handoffReason,omitempty"`
	ResumeState              State                `json:"-"`
	ResumeAfterRevision      int64                `json:"resumeAfterObservationRevision,omitempty"`
	FreshObservationRequired bool                 `json:"freshObservationRequired"`
	Result                   *ResultVerification  `json:"resultVerification,omitempty"`
	CreatedAt                time.Time            `json:"createdAt"`
	UpdatedAt                time.Time            `json:"updatedAt"`
	TerminalAt               *time.Time           `json:"terminalAt,omitempty"`
}

func NewRun(id string, candidate CandidateReference, now time.Time) (Run, error) {
	if strings.TrimSpace(id) == "" || candidate.ProductURL == "" || now.IsZero() {
		return Run{}, ErrInvalid
	}
	now = now.UTC()
	return Run{
		ID: strings.TrimSpace(id), UserID: candidate.UserID,
		CurationID: candidate.CurationID, CandidateID: candidate.CandidateID,
		ProductURL: candidate.ProductURL, MerchantOrigin: candidate.MerchantOrigin,
		MerchantHost: candidate.MerchantHost,
		State:        StateAwaitingNavigationApproval, ControlOwner: ControlNone,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (r *Run) ApproveNavigation(now time.Time) error {
	if r.State != StateAwaitingNavigationApproval {
		return ErrInvalidTransition
	}
	now = now.UTC()
	r.State, r.ControlOwner = StateNavigationApproved, ControlAgent
	r.NavigationApprovedAt = &now
	r.touch(now)
	return nil
}

func (r *Run) ApprovePreparation(approval PreparationApproval, now time.Time) error {
	if r.State != StateAwaitingPreparationApproval {
		return ErrInvalidTransition
	}
	steps, err := validatePreparationApproval(approval)
	if err != nil {
		return err
	}
	now = now.UTC()
	approval.AllowedPreparationSteps = steps
	approval.PriceCurrency = strings.ToUpper(strings.TrimSpace(approval.PriceCurrency))
	approval.ApprovedAt = now
	r.Preparation = &approval
	r.State, r.ControlOwner = StatePreparationApproved, ControlAgent
	r.touch(now)
	return nil
}

func validatePreparationApproval(value PreparationApproval) ([]PreparationStep, error) {
	if value.PlanRevision < 1 || !validDigest(value.QuoteDigest) || value.PriceCeilingMinor < 1 ||
		!currencyPattern.MatchString(strings.ToUpper(strings.TrimSpace(value.PriceCurrency))) ||
		len(value.AllowedPreparationSteps) == 0 || len(value.AllowedPreparationSteps) > 4 {
		return nil, fmt.Errorf("%w: preparation approval", ErrInvalid)
	}
	allowed := map[PreparationStep]bool{
		StepSelectVariant: true, StepSetQuantity: true,
		StepAddToCart: true, StepOpenCheckoutReview: true,
	}
	seen := map[PreparationStep]bool{}
	steps := append([]PreparationStep(nil), value.AllowedPreparationSteps...)
	for _, step := range steps {
		if !allowed[step] || seen[step] {
			return nil, fmt.Errorf("%w: preparation step %q", ErrInvalid, step)
		}
		seen[step] = true
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i] < steps[j] })
	return steps, nil
}

func (r *Run) RecordObservation(observation Observation, sanitized bool, containsSensitiveData bool, now time.Time) error {
	if r.terminal() || r.State == StateAwaitingNavigationApproval || r.State == StatePaused ||
		r.State == StateUserControl {
		return ErrInvalidTransition
	}
	if !sanitized || containsSensitiveData || observation.Revision != r.latestObservationRevision()+1 ||
		!validDigest(observation.PageIdentityDigest) {
		return fmt.Errorf("%w: observation envelope", ErrInvalid)
	}
	if observation.SessionStateHint == "" {
		observation.SessionStateHint = SessionUnknown
	}
	switch observation.SessionStateHint {
	case SessionAuthenticated, SessionAnonymous, SessionUnknown:
	default:
		return fmt.Errorf("%w: session state hint", ErrInvalid)
	}
	origin, err := CanonicalOrigin(observation.Origin)
	if err != nil {
		return err
	}
	switch observation.Kind {
	case ObservationPublicProduct, ObservationSignInRequired, ObservationCheckoutReview, ObservationResult:
	default:
		return fmt.Errorf("%w: observation kind", ErrInvalid)
	}
	now = now.UTC()
	observation.Origin, observation.ObservedAt = origin, now
	r.LatestObservation = &observation

	switch observation.Kind {
	case ObservationSignInRequired:
		if r.State != StateResumeRequiresObservation {
			r.ResumeState = r.State
		}
		r.State, r.ControlOwner = StateUserControl, ControlUser
		r.HandoffReason = HandoffSignInRequired
		r.ResumeAfterRevision = observation.Revision
		r.FreshObservationRequired = false
	case ObservationCheckoutReview:
		if r.Preparation == nil {
			return ErrInvalidTransition
		}
		r.State, r.ControlOwner = StateReadyForUserPayment, ControlUser
		r.HandoffReason = HandoffFinalPaymentRequired
		r.ResumeState, r.ResumeAfterRevision = "", 0
		r.FreshObservationRequired = false
	case ObservationPublicProduct:
		if origin != r.MerchantOrigin {
			return fmt.Errorf("%w: public product origin changed", ErrInvalid)
		}
		switch r.State {
		case StateNavigationApproved:
			r.State, r.ControlOwner = StateAwaitingPreparationApproval, ControlNone
		case StateResumeRequiresObservation:
			if observation.Revision <= r.ResumeAfterRevision || !resumableState(r.ResumeState) {
				return ErrInvalidTransition
			}
			r.State = r.ResumeState
			if r.State == StateAwaitingPreparationApproval {
				r.ControlOwner = ControlNone
			} else {
				r.ControlOwner = ControlAgent
			}
			r.ResumeState, r.ResumeAfterRevision = "", 0
			r.HandoffReason, r.FreshObservationRequired = "", false
		}
	case ObservationResult:
		if r.State != StateReadyForUserPayment && r.State != StateResumeRequiresObservation {
			return ErrInvalidTransition
		}
	}
	r.touch(now)
	return nil
}

func (r *Run) RequireHandoff(reason HandoffReason, now time.Time) error {
	if r.terminal() || r.State == StateAwaitingNavigationApproval || r.State == StateUserControl ||
		r.State == StatePaused || r.State == StateResumeRequiresObservation {
		return ErrInvalidTransition
	}
	switch reason {
	case HandoffSignInRequired, HandoffVerificationRequired, HandoffUnsupportedInteraction:
		r.ResumeState = r.State
		r.State, r.ControlOwner = StateUserControl, ControlUser
		r.HandoffReason = reason
		r.ResumeAfterRevision = r.latestObservationRevision()
	case HandoffFinalPaymentRequired:
		if r.Preparation == nil {
			return ErrInvalidTransition
		}
		r.State, r.ControlOwner = StateReadyForUserPayment, ControlUser
		r.HandoffReason = reason
		r.ResumeState, r.ResumeAfterRevision = "", 0
	default:
		return fmt.Errorf("%w: handoff reason", ErrInvalid)
	}
	r.FreshObservationRequired = false
	r.touch(now.UTC())
	return nil
}

func (r *Run) TakeOver(now time.Time) error {
	if r.terminal() || r.State == StateAwaitingNavigationApproval || r.State == StateUserControl ||
		r.State == StateReadyForUserPayment {
		return ErrInvalidTransition
	}
	if r.State != StateResumeRequiresObservation && r.State != StatePaused {
		r.ResumeState = r.State
	}
	r.State, r.ControlOwner = StateUserControl, ControlUser
	r.HandoffReason = HandoffUserRequested
	r.ResumeAfterRevision = r.latestObservationRevision()
	r.FreshObservationRequired = false
	r.touch(now.UTC())
	return nil
}

func (r *Run) Pause(now time.Time) error {
	if r.terminal() || r.State == StateAwaitingNavigationApproval || r.State == StatePaused ||
		r.State == StateUserControl || r.State == StateReadyForUserPayment {
		return ErrInvalidTransition
	}
	if r.State != StateResumeRequiresObservation {
		r.ResumeState = r.State
	}
	r.State, r.ControlOwner = StatePaused, ControlNone
	r.HandoffReason = ""
	r.ResumeAfterRevision = r.latestObservationRevision()
	r.FreshObservationRequired = false
	r.touch(now.UTC())
	return nil
}

// RequestResume never restores agent control by itself. A later, sanitized
// observation from the merchant origin must be recorded first.
func (r *Run) RequestResume(now time.Time) error {
	if (r.State != StateUserControl && r.State != StatePaused) ||
		r.HandoffReason == HandoffFinalPaymentRequired || !resumableState(r.ResumeState) {
		return ErrInvalidTransition
	}
	r.State, r.ControlOwner = StateResumeRequiresObservation, ControlNone
	r.ResumeAfterRevision = r.latestObservationRevision()
	r.FreshObservationRequired = true
	r.touch(now.UTC())
	return nil
}

func (r *Run) Cancel(now time.Time) error {
	if r.terminal() {
		return ErrInvalidTransition
	}
	now = now.UTC()
	r.State, r.ControlOwner = StateCancelled, ControlNone
	r.ResumeState, r.HandoffReason = "", ""
	r.ResumeAfterRevision, r.FreshObservationRequired = 0, false
	r.TerminalAt = &now
	r.touch(now)
	return nil
}

func (r *Run) VerifyResult(verification ResultVerification, now time.Time) error {
	if r.State != StateReadyForUserPayment && r.State != StateUserControl &&
		r.State != StateResumeRequiresObservation {
		return ErrInvalidTransition
	}
	switch verification.Outcome {
	case OutcomeSucceeded, OutcomePartial, OutcomeFailed, OutcomeUnknown:
	default:
		return fmt.Errorf("%w: result outcome", ErrInvalid)
	}
	switch verification.EvidenceSource {
	case EvidenceMerchantObserved:
		if r.LatestObservation == nil || r.LatestObservation.Kind != ObservationResult ||
			r.LatestObservation.Origin != r.MerchantOrigin ||
			verification.ObservationRevision != r.LatestObservation.Revision ||
			!validDigest(verification.EvidenceDigest) {
			return fmt.Errorf("%w: merchant result evidence", ErrInvalid)
		}
	case EvidenceUserReported:
		if verification.EvidenceDigest != "" || verification.ObservationRevision != 0 {
			return fmt.Errorf("%w: user report cannot claim merchant observation", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: result evidence source", ErrInvalid)
	}
	now = now.UTC()
	verification.VerifiedAt = now
	r.Result = &verification
	r.ControlOwner, r.ResumeState, r.HandoffReason = ControlNone, "", ""
	r.ResumeAfterRevision, r.FreshObservationRequired = 0, false
	switch verification.Outcome {
	case OutcomeSucceeded:
		r.State = StateCompleted
	case OutcomePartial:
		r.State = StatePartial
	case OutcomeFailed:
		r.State = StateFailed
	case OutcomeUnknown:
		r.State = StateResultUnknown
	}
	r.TerminalAt = &now
	r.touch(now)
	return nil
}

func (r *Run) CheckExpectedVersion(expected int64) error {
	if expected < 1 || expected != r.Version {
		return ErrVersionConflict
	}
	return nil
}

func (r *Run) touch(now time.Time) {
	r.Version++
	r.UpdatedAt = now.UTC()
}

func (r Run) terminal() bool {
	switch r.State {
	case StateCompleted, StatePartial, StateFailed, StateResultUnknown, StateCancelled:
		return true
	default:
		return false
	}
}

func (r Run) latestObservationRevision() int64 {
	if r.LatestObservation == nil {
		return 0
	}
	return r.LatestObservation.Revision
}

func resumableState(state State) bool {
	switch state {
	case StateNavigationApproved, StateAwaitingPreparationApproval, StatePreparationApproved:
		return true
	default:
		return false
	}
}

func validDigest(value string) bool { return digestPattern.MatchString(strings.TrimSpace(value)) }

func CanonicalMerchantURL(raw string) (canonical, origin, host string, err error) {
	parsed, parseErr := url.Parse(strings.TrimSpace(raw))
	if parseErr != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" ||
		(parsed.Port() != "" && parsed.Port() != "443") {
		return "", "", "", fmt.Errorf("%w: merchant URL must be public HTTPS", ErrInvalid)
	}
	host = strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if !publicHostname(host) {
		return "", "", "", fmt.Errorf("%w: merchant URL must use a public hostname", ErrInvalid)
	}
	parsed.Scheme, parsed.Host, parsed.Fragment = "https", host, ""
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), "https://" + host, host, nil
}

func CanonicalOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" ||
		(parsed.Port() != "" && parsed.Port() != "443") || parsed.Path != "" && parsed.Path != "/" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: origin", ErrInvalid)
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if !publicHostname(host) {
		return "", fmt.Errorf("%w: origin", ErrInvalid)
	}
	return "https://" + host, nil
}

func publicHostname(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") ||
		!strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}
