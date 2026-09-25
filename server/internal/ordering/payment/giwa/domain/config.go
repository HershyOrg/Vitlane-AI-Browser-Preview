package domain

// GenericWebUSDSettlementPathID identifies the single TEST settlement
// principal used by AgencyOrder.
const GenericWebUSDSettlementPathID = "GENERIC_WEB_USD"

type MerchantRegistryEntry struct {
	MerchantID         string   `json:"merchantId"`
	DisplayName        string   `json:"displayName"`
	DomainSuffixes     []string `json:"domainSuffixes"`
	Country            string   `json:"country"`
	Currency           string   `json:"currency"`
	FulfillmentMode    string   `json:"fulfillmentMode"`
	PaymentEnabled     bool     `json:"paymentEnabled"`
	PrincipalRecipient string   `json:"principalRecipient"`
	RegistryVersion    uint64   `json:"registryVersion"`
	Active             bool     `json:"active"`
}

type SettlementConfig struct {
	Environment       string `json:"environment"`
	ChainID           uint64 `json:"chainId"`
	ChainCAIP2        string `json:"chainCaip2"`
	RPCURL            string `json:"rpcUrl"`
	ExplorerURL       string `json:"explorerUrl"`
	TokenAddress      string `json:"tokenAddress"`
	FaucetAddress     string `json:"faucetAddress"`
	SettlementAddress string `json:"settlementAddress"`
	TokenSymbol       string `json:"tokenSymbol"`
	TokenDecimals     uint8  `json:"tokenDecimals"`
	FeeBps            uint16 `json:"feeBps"`
	FeeRecipient      string `json:"feeRecipient"`
	ClaimAmount       string `json:"claimAmountBaseUnits"`
}
