package domain

import curation "github.com/vitlane/vitlane/server/internal/curation/domain"

// Before any Target exists there is one eligible primitive: initialize the
// curation. That primitive obtains the complete target/criteria/budget proposal
// in one structured call and commits its configuration atomically.
func InitialActionDecisions(actionID string) []curation.ActionDecision {
	return []curation.ActionDecision{{ID: actionID + ":route", Kind: "ACTION", Result: "INITIALIZE", Source: "DETERMINISTIC", ReasonCode: "INITIAL_CONFIGURATION_REQUIRED"}, {ID: actionID + ":target", Kind: "TARGET", Result: "COMMAND_SCOPE", Source: "DETERMINISTIC", ReasonCode: "NO_TARGETS_YET"}, {ID: actionID + ":conditions", Kind: "CONDITIONS", Result: "DEFERRED", Source: "DETERMINISTIC", ReasonCode: "INITIAL_CONFIGURATION_PRIMITIVE"}, {ID: actionID + ":budget", Kind: "BUDGET", Result: "DEFERRED", Source: "DETERMINISTIC", ReasonCode: "INITIAL_CONFIGURATION_PRIMITIVE"}}
}
