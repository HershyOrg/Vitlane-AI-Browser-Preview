package adapters

import (
	"errors"
	"testing"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

func TestTranslateShippingErrorMakesMissingDefaultActionable(t *testing.T) {
	if got := translateShippingError(accountdomain.ErrShippingProfileMissing); !errors.Is(got, agencydomain.ErrShippingRequired) {
		t.Fatalf("error=%v", got)
	}
	original := errors.New("storage unavailable")
	if got := translateShippingError(original); !errors.Is(got, original) {
		t.Fatalf("unexpected error translation: %v", got)
	}
}
