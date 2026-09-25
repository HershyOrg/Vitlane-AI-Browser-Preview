package domain

// NormalizeProductVertical preserves a single coarse routing family. Empty is
// retained for legacy targets until their next query supplies a classification.
func NormalizeProductVertical(value string) string {
	switch value {
	case "", "GENERAL", "FASHION", "BEAUTY", "FOOD", "LIVING", "ELECTRONICS":
		return value
	}
	return "GENERAL"
}
