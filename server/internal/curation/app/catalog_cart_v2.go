package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

var ErrCatalogCartVersionConflict = errors.New("PHASE8_CART_VERSION_CONFLICT")

type CatalogCartItemV2 struct {
	TargetID     string
	Merchant     string
	SellerDomain string
	IntentPoint  string
	Item         curationdomain.CartItemV2
}

type CatalogCartStateV2 struct {
	UserID     string
	CurationID string
	Version    int64
	Country    string
	Currency   string
	Items      []CatalogCartItemV2
	UpdatedAt  time.Time
}

type CatalogCartRepositoryV2 interface {
	GetCatalogCartV2(context.Context, string, string) (CatalogCartStateV2, error)
	ReplaceCatalogCartV2(
		context.Context,
		string,
		string,
		int64,
		[]CatalogCartItemV2,
		time.Time,
	) (CatalogCartStateV2, error)
}

type CatalogCartServiceV2 struct {
	repository CatalogCartRepositoryV2
	clock      sharedapp.Clock
}

func NewCatalogCartServiceV2(
	repository CatalogCartRepositoryV2,
	clock sharedapp.Clock,
) (*CatalogCartServiceV2, error) {
	if repository == nil || clock == nil {
		return nil, fault.New(fault.InvalidInput, "PHASE8_CART_CONFIG_INVALID", false)
	}
	return &CatalogCartServiceV2{repository: repository, clock: clock}, nil
}

func (service *CatalogCartServiceV2) Get(
	ctx context.Context,
	userID, curationID string,
) (CatalogCartStateV2, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(curationID) == "" {
		return CatalogCartStateV2{}, fault.New(fault.InvalidInput, "PHASE8_CART_READ_INVALID", false)
	}
	return service.repository.GetCatalogCartV2(ctx, userID, curationID)
}

func (service *CatalogCartServiceV2) Replace(
	ctx context.Context,
	userID, curationID string,
	expectedVersion int64,
	items []CatalogCartItemV2,
) (CatalogCartStateV2, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(curationID) == "" ||
		expectedVersion < 0 || len(items) > 10 {
		return CatalogCartStateV2{}, fault.New(fault.InvalidInput, "PHASE8_CART_REPLACE_INVALID", false)
	}
	seen := make(map[string]struct{}, len(items))
	now := service.clock.Now()
	validated := make([]CatalogCartItemV2, 0, len(items))
	for _, item := range items {
		item.TargetID = strings.TrimSpace(item.TargetID)
		item.Merchant = strings.TrimSpace(item.Merchant)
		item.SellerDomain = strings.ToLower(strings.TrimSpace(item.SellerDomain))
		item.IntentPoint = strings.TrimSpace(item.IntentPoint)
		if item.TargetID == "" || len(item.Merchant) > 240 ||
			len(item.SellerDomain) > 255 || len(item.IntentPoint) > 1000 {
			return CatalogCartStateV2{}, fault.New(fault.InvalidInput, "PHASE8_CART_ITEM_INVALID", false)
		}
		item.Item.AddedAt = now
		valid, err := curationdomain.NewCartItemV2(item.Item)
		if err != nil {
			// 실패 필드를 reason에 붙여 간헐 결함의 원인(미수화 후보 데이터 등)을
			// 클라이언트 로그만으로 특정할 수 있게 한다.
			return CatalogCartStateV2{}, fault.New(
				fault.InvalidInput,
				"PHASE8_CART_ITEM_INVALID_"+curationdomain.CartItemV2InvalidField(item.Item),
				false,
			)
		}
		identity := valid.CandidateID + "\x00" + valid.VariantID
		if _, exists := seen[identity]; exists {
			return CatalogCartStateV2{}, fault.New(fault.InvalidInput, "PHASE8_CART_ITEM_DUPLICATED", false)
		}
		seen[identity] = struct{}{}
		item.Item = valid
		validated = append(validated, item)
	}
	state, err := service.repository.ReplaceCatalogCartV2(
		ctx, userID, curationID, expectedVersion, validated, now,
	)
	if errors.Is(err, ErrCatalogCartVersionConflict) {
		return CatalogCartStateV2{}, fault.New(fault.Conflict, "PHASE8_CART_VERSION_CONFLICT", true)
	}
	if err == nil {
		sharedapp.RecordAnalytics(ctx, sharedapp.AnalyticsEvent{Name: "cart_updated", UserID: userID, Key: curationID + ":" + strconv.FormatInt(state.Version, 10), CurationID: curationID, Action: "SET"})
	}
	return state, err
}
