package domain

import "time"

// ProductReaction is a fallback preference for an observed original product.
// It never supplies a Variant identity or grants configuration/cart authority.
type ProductReaction struct {
	TargetID      string                      `json:"targetId"`
	CandidateID   string                      `json:"candidateId"`
	ProductRef    SourceProductRef            `json:"productRef"`
	Pinned        bool                        `json:"pinned"`
	Sentiment     string                      `json:"sentiment"`
	Version       int64                       `json:"version"`
	UpdatedAt     time.Time                   `json:"updatedAt"`
	LikedSnapshot *ExternalProductObservation `json:"-"`
}

func ProductReactionFallbackAllowed(observation *ExternalProductObservation, hasVariant bool) bool {
	return !hasVariant && observation != nil && observation.ProductRef.Source.KoreanExternal() && observation.Validate() == nil
}

func (r ProductReaction) Validate() error {
	if !r.ProductRef.Source.KoreanExternal() || r.ProductRef.Validate() != nil || r.TargetID == "" || r.CandidateID == "" || r.Version < 0 || r.UpdatedAt.IsZero() {
		return ErrSourceReference
	}
	if r.Sentiment != "NONE" && r.Sentiment != "LIKE" && r.Sentiment != "DISLIKE" {
		return ErrSourceReference
	}
	if r.Sentiment == "LIKE" {
		if r.LikedSnapshot == nil || r.LikedSnapshot.Validate() != nil || r.LikedSnapshot.ProductRef != r.ProductRef {
			return ErrSourceReference
		}
	} else if r.LikedSnapshot != nil {
		return ErrSourceReference
	}
	return nil
}

type LikedProduct struct {
	CurationID  string                     `json:"curationId"`
	CandidateID string                     `json:"candidateId"`
	Observation ExternalProductObservation `json:"observation"`
	UpdatedAt   time.Time                  `json:"updatedAt"`
}
