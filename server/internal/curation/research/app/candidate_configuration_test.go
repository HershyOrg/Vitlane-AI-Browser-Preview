package app

import (
	"context"
	"errors"
	"testing"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

type candidateConfigurationTransactor struct {
	calls int
}

func (t *candidateConfigurationTransactor) WithinTransaction(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	t.calls++
	return fn(ctx)
}

func TestPrepareCandidateConfigurationOwnsOneTransaction(t *testing.T) {
	service, repository, _, _, now := newSubmitFixture(t)
	candidate := seedHistoricalCandidate(t, repository, now)
	transactor := &candidateConfigurationTransactor{}
	service.transactor = transactor

	configuration, err := service.PrepareCandidateConfiguration(
		context.Background(),
		PrepareCandidateConfigurationInput{
			UserID: "user-1", SessionID: "session-1",
			CandidateID: candidate.ID,
			Configuration: researchdomain.CandidateConfigurationInput{
				Fields:            []researchdomain.VariantField{},
				Selections:        map[string]string{},
				ConfirmsNoOptions: true,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if transactor.calls != 1 ||
		configuration.CandidateID != candidate.ID ||
		configuration.UserID != "user-1" ||
		len(repository.configurations) != 1 {
		t.Fatalf(
			"transaction=%d configuration=%#v stored=%#v",
			transactor.calls, configuration, repository.configurations,
		)
	}
}

func TestPrepareCandidateConfigurationRejectsUnauthorizedOrInvalidInput(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(
			*PrepareCandidateConfigurationInput,
			*memoryShoppingRepository,
		)
		want error
	}{
		{
			name: "different owner",
			mutate: func(
				input *PrepareCandidateConfigurationInput,
				_ *memoryShoppingRepository,
			) {
				input.UserID = "user-2"
			},
			want: shoppingsessiondomain.ErrSessionNotFound,
		},
		{
			name: "session no longer reviewing",
			mutate: func(
				_ *PrepareCandidateConfigurationInput,
				shopping *memoryShoppingRepository,
			) {
				shopping.session.Status =
					shoppingsessiondomain.SessionStatusReady
			},
			want: shoppingsessiondomain.ErrCandidateNotPurchasable,
		},
		{
			name: "incomplete configuration",
			mutate: func(
				input *PrepareCandidateConfigurationInput,
				_ *memoryShoppingRepository,
			) {
				input.Configuration.ConfirmsNoOptions = false
			},
			want: researchdomain.ErrConfigurationInvalid,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			service, repository, shopping, _, now := newSubmitFixture(t)
			candidate := seedHistoricalCandidate(t, repository, now)
			transactor := &candidateConfigurationTransactor{}
			service.transactor = transactor
			input := PrepareCandidateConfigurationInput{
				UserID: "user-1", SessionID: "session-1",
				CandidateID: candidate.ID,
				Configuration: researchdomain.CandidateConfigurationInput{
					Fields:            []researchdomain.VariantField{},
					Selections:        map[string]string{},
					ConfirmsNoOptions: true,
				},
			}
			test.mutate(&input, shopping)

			_, err := service.PrepareCandidateConfiguration(
				context.Background(), input,
			)

			if !errors.Is(err, test.want) ||
				transactor.calls != 1 ||
				len(repository.configurations) != 0 {
				t.Fatalf(
					"err=%v transaction=%d stored=%#v",
					err, transactor.calls, repository.configurations,
				)
			}
		})
	}
}
