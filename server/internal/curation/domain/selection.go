package domain

import (
	"errors"
	"strings"
	"time"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

var (
	ErrSelectionNotFound        = errors.New("CURATION_SELECTION_NOT_FOUND")
	ErrSelectionInvalid         = errors.New("CURATION_SELECTION_INVALID")
	ErrSelectionVersionConflict = errors.New("CURATION_SELECTION_VERSION_CONFLICT")
	ErrSelectionCommandConflict = errors.New("CURATION_SELECTION_COMMAND_CONFLICT")
)

const (
	minSelectionQuantity int64 = 1
	maxSelectionQuantity int64 = 99
)

// CurationSelection is the user's current configured Candidate choice.
// It has no purchase or cart lifecycle. Removal is retained only as immutable
// history and active selections are projected as CartView.
type CurationSelection struct {
	ID                         string     `json:"id"`
	UserID                     string     `json:"-"`
	CurationID                 string     `json:"curationId"`
	PlanTargetID               string     `json:"targetId"`
	ShoppingSessionID          string     `json:"shoppingSessionId"`
	CandidateID                string     `json:"candidateId"`
	CandidateConfigurationID   string     `json:"candidateConfigurationId"`
	CandidateConfigurationHash string     `json:"configurationHash"`
	Quantity                   int64      `json:"quantity"`
	Version                    int64      `json:"version"`
	SelectedAt                 time.Time  `json:"selectedAt"`
	UpdatedAt                  time.Time  `json:"updatedAt"`
	RemovedAt                  *time.Time `json:"removedAt,omitempty"`
}

func NewCurationSelection(
	id, userID, curationID, targetID, sessionID, candidateID,
	configurationID, configurationHash string,
	quantity int64,
	now time.Time,
) (CurationSelection, error) {
	if !validSelectionIdentity(
		id, userID, curationID, targetID, sessionID, candidateID,
		configurationID, configurationHash,
	) || !validSelectionQuantity(quantity) || now.IsZero() {
		return CurationSelection{}, ErrSelectionInvalid
	}
	return CurationSelection{
		ID:                         id,
		UserID:                     userID,
		CurationID:                 curationID,
		PlanTargetID:               targetID,
		ShoppingSessionID:          sessionID,
		CandidateID:                candidateID,
		CandidateConfigurationID:   configurationID,
		CandidateConfigurationHash: configurationHash,
		Quantity:                   quantity,
		Version:                    1,
		SelectedAt:                 now,
		UpdatedAt:                  now,
	}, nil
}

// Revise changes only the replaceable configuration snapshot and quantity.
// Curation, Target, Session and Candidate lineage remain immutable.
func (s *CurationSelection) Revise(
	configurationID, configurationHash string,
	quantity, expectedVersion int64,
	now time.Time,
) error {
	if s.RemovedAt != nil || s.Version != expectedVersion {
		return ErrSelectionVersionConflict
	}
	if strings.TrimSpace(configurationID) == "" ||
		strings.TrimSpace(configurationHash) == "" ||
		!validSelectionQuantity(quantity) ||
		now.IsZero() || now.Before(s.UpdatedAt) {
		return ErrSelectionInvalid
	}
	s.CandidateConfigurationID = configurationID
	s.CandidateConfigurationHash = configurationHash
	s.Quantity = quantity
	s.Version++
	s.UpdatedAt = now
	return nil
}

// Remove is a soft removal so AgencyOrder provenance can continue to reference
// the exact Selection snapshot that existed when a request was sealed.
func (s *CurationSelection) Remove(
	expectedVersion int64,
	now time.Time,
) error {
	if s.RemovedAt != nil || s.Version != expectedVersion {
		return ErrSelectionVersionConflict
	}
	if now.IsZero() || now.Before(s.UpdatedAt) {
		return ErrSelectionInvalid
	}
	s.Version++
	s.UpdatedAt = now
	s.RemovedAt = &now
	return nil
}

func validSelectionIdentity(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func validSelectionQuantity(quantity int64) bool {
	return quantity >= minSelectionQuantity && quantity <= maxSelectionQuantity
}

type CartViewCandidate struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProductURL string `json:"productUrl"`
	ImageURL   string `json:"imageUrl,omitempty"`
}

type CartSelection struct {
	Selection CurationSelection  `json:"selection"`
	Candidate CartViewCandidate  `json:"candidate"`
	UnitPrice shareddomain.Money `json:"unitPrice"`
	LineTotal shareddomain.Money `json:"lineTotal"`
}

type CartWarning struct {
	Code        string `json:"code"`
	SelectionID string `json:"selectionId,omitempty"`
	Message     string `json:"message"`
}

// CartView is a projection only. An empty view is valid and has no status.
type CartView struct {
	CurationID string             `json:"curationId"`
	Selections []CartSelection    `json:"selections"`
	Total      shareddomain.Money `json:"total"`
	Warnings   []CartWarning      `json:"warnings"`
	UpdatedAt  time.Time          `json:"updatedAt"`
}
