package app

import (
	"regexp"
	"strings"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// Storefront cart는 배송지·전화 형식에 관대하지만 Shopify UCP create_checkout은
// 엄격하게 검증하고 isError로 거절한다. provider 왕복 전에 같은 규칙으로
// fail-fast해야 사용자가 원인 필드를 즉시 교정할 수 있다.
var (
	usZIPPattern   = regexp.MustCompile(`^\d{5}(-\d{4})?$`)
	usPhoneDigits  = regexp.MustCompile(`^\+?1?([2-9]\d{2}[2-9]\d{6})$`)
	usPhoneStrip   = regexp.MustCompile(`[\s().-]`)
	usRegionCodes  = map[string]bool{}
	usRegionSource = "AL AK AZ AR CA CO CT DE FL GA HI ID IL IN IA KS KY LA ME MD MA MI MN MS MO MT NE NV NH NJ NM NY NC ND OH OK OR PA RI SC SD TN TX UT VT VA WA WV WI WY DC"
)

func init() {
	for code := range strings.FieldsSeq(usRegionSource) {
		usRegionCodes[code] = true
	}
}

// NormalizeUSShippingAddress는 미국 배송지 입력을 검증하고 provider 계약이
// 기대하는 형태(주 코드 대문자 2자, ZIP 5/9, E.164 +1 전화)로 정규화한다.
// 실패 reason은 필드 단위로 구분해 웹이 해당 입력을 직접 지목할 수 있게 한다.
func NormalizeUSShippingAddress(address ShippingAddress) (ShippingAddress, error) {
	address.RecipientName = strings.TrimSpace(address.RecipientName)
	address.AddressLine1 = strings.TrimSpace(address.AddressLine1)
	address.AddressLine2 = strings.TrimSpace(address.AddressLine2)
	address.City = strings.TrimSpace(address.City)
	address.Region = strings.ToUpper(strings.TrimSpace(address.Region))
	address.PostalCode = strings.TrimSpace(address.PostalCode)
	address.Country = strings.ToUpper(strings.TrimSpace(address.Country))
	address.Phone = usPhoneStrip.ReplaceAllString(strings.TrimSpace(address.Phone), "")

	if address.Country != "US" {
		return ShippingAddress{}, fault.New(fault.InvalidInput, "SHIPPING_ADDRESS_US_ONLY", false)
	}
	if address.RecipientName == "" || len(address.RecipientName) > 120 ||
		address.AddressLine1 == "" || len(address.AddressLine1) > 200 ||
		len(address.AddressLine2) > 200 ||
		address.City == "" || len(address.City) > 100 {
		return ShippingAddress{}, fault.New(fault.InvalidInput, "SHIPPING_ADDRESS_FIELDS_INVALID", false)
	}
	if !usRegionCodes[address.Region] {
		return ShippingAddress{}, fault.New(fault.InvalidInput, "SHIPPING_REGION_US_FORMAT", false)
	}
	if !usZIPPattern.MatchString(address.PostalCode) {
		return ShippingAddress{}, fault.New(fault.InvalidInput, "SHIPPING_POSTAL_US_FORMAT", false)
	}
	match := usPhoneDigits.FindStringSubmatch(address.Phone)
	if match == nil {
		return ShippingAddress{}, fault.New(fault.InvalidInput, "SHIPPING_PHONE_US_FORMAT", false)
	}
	address.Phone = "+1" + match[1]
	return address, nil
}
