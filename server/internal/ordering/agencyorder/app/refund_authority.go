package app

// RefundAuthority contains immutable reducer-issued facts, never a live Owner read.
type RefundAuthority struct {
	AllocationID, MerchantState, FundingState string
	HasCompensation                           bool
}
