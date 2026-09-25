package domain_test

import (
	"testing"

	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
)

func defaultRegistry(t *testing.T) runnerdomain.Registry {
	t.Helper()
	registry, err := runnerdomain.NewRegistry(
		runnerdomain.DefaultModels(), runnerdomain.DefaultModelKey,
	)
	if err != nil {
		t.Fatalf("build default registry: %v", err)
	}
	return registry
}

func TestDefaultRegistryIsValidAndDefaultsToLuna(t *testing.T) {
	registry := defaultRegistry(t)

	selected, err := registry.Lookup(runnerdomain.DefaultModelKey)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Key != "gpt-5.6-luna" {
		t.Fatalf("default model=%q, want Luna", selected.Key)
	}
}

// A full curation is one planning call plus two calls per research round. These
// figures are what the $0.10 per-user cap has to accommodate, so a pricing or
// max-output change that breaks the budget should fail here rather than in
// production.
func TestOneCurationFitsComfortablyInsideTheUserDailyCap(t *testing.T) {
	registry := defaultRegistry(t)
	limits := runnerdomain.DefaultLimits()

	for _, key := range []string{"gpt-5-nano", "gpt-5.6-luna"} {
		model, err := registry.Lookup(key)
		if err != nil {
			t.Fatal(err)
		}
		planning := model.Cost(runnerdomain.TokenUsage{
			InputTokens: 1_500, OutputTokens: 400,
		})
		interpret := model.Cost(runnerdomain.TokenUsage{
			InputTokens: 1_500, OutputTokens: 200,
		})
		rank := model.Cost(runnerdomain.TokenUsage{
			InputTokens: 4_000, OutputTokens: 1_500,
		})
		// One planning call and three researched targets.
		curation := planning + 3*(interpret+rank)
		if curation >= limits.UserDaily {
			t.Fatalf(
				"%s: one curation costs %d micros, at or over the %d cap",
				key, curation, limits.UserDaily,
			)
		}
		if curation*5 >= limits.UserDaily {
			t.Fatalf(
				"%s: five curations cost %d micros, at or over the %d cap; "+
					"a user should get several attempts per day",
				key, curation*5, limits.UserDaily,
			)
		}
	}
}

// ADR-0032 promises at least 100 users a day. That only holds if the average
// user costs well under the arithmetic worst case of cap x users.
func TestServerCapServesAtLeastOneHundredUsers(t *testing.T) {
	registry := defaultRegistry(t)
	limits := runnerdomain.DefaultLimits()
	model, err := registry.Lookup("gpt-5.6-luna")
	if err != nil {
		t.Fatal(err)
	}

	// One curation on the expensive model, three researched targets.
	perUser := model.Cost(runnerdomain.TokenUsage{
		InputTokens: 1_500, OutputTokens: 400,
	}) + 3*(model.Cost(runnerdomain.TokenUsage{
		InputTokens: 1_500, OutputTokens: 200,
	})+model.Cost(runnerdomain.TokenUsage{
		InputTokens: 4_000, OutputTokens: 1_500,
	}))
	if perUser*100 > limits.ServerAdmission {
		t.Fatalf(
			"100 users at %d micros each need %d, over the %d admission line",
			perUser, perUser*100, limits.ServerAdmission,
		)
	}
}

// The reservation is what makes the cap a hard limit, so it must bound any
// response the provider can actually return.
func TestWorstCaseReservationStillLeavesRoomForSeveralCalls(t *testing.T) {
	registry := defaultRegistry(t)
	limits := runnerdomain.DefaultLimits()
	model, err := registry.Lookup("gpt-5.6-luna")
	if err != nil {
		t.Fatal(err)
	}

	worst := model.WorstCost(6_000)
	if worst <= 0 {
		t.Fatal("worst-case reservation must be positive")
	}
	// A single research round takes two reservations. If one worst case ate the
	// whole cap, the first round could never finish.
	if worst*2 >= limits.UserDaily {
		t.Fatalf(
			"two worst-case reservations cost %d micros, at or over the %d cap",
			worst*2, limits.UserDaily,
		)
	}
}
