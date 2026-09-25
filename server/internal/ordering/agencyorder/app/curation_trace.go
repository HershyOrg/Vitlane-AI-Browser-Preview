package app

import "time"

// CurationTrace is the read-only lineage from a Curation target/candidate to
// the AgencyOrder snapshot that replaced Purchase as the active order record.
type CurationTrace struct {
	AgencyOrderID string    `json:"agencyOrderId"`
	CurationID    string    `json:"curationId"`
	TargetID      string    `json:"targetId"`
	CandidateID   string    `json:"candidateId"`
	State         string    `json:"state"`
	IssuedAt      time.Time `json:"issuedAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
