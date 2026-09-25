package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

func TestSelectionErrorsUseActionableHTTPStatuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			name:   "curation not found",
			err:    curationdomain.ErrCurationNotFound,
			status: http.StatusNotFound,
			code:   "CURATION_NOT_FOUND",
		},
		{
			name:   "selection not found",
			err:    curationdomain.ErrSelectionNotFound,
			status: http.StatusNotFound,
			code:   "CURATION_SELECTION_NOT_FOUND",
		},
		{
			name:   "stale version",
			err:    curationdomain.ErrSelectionVersionConflict,
			status: http.StatusConflict,
			code:   "CURATION_SELECTION_VERSION_CONFLICT",
		},
		{
			name:   "command conflict",
			err:    curationdomain.ErrSelectionCommandConflict,
			status: http.StatusConflict,
			code:   "CURATION_SELECTION_COMMAND_CONFLICT",
		},
		{
			name:   "invalid configuration",
			err:    researchdomain.ErrConfigurationInvalid,
			status: http.StatusUnprocessableEntity,
			code:   "CANDIDATE_CONFIGURATION_INVALID",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			writeSelectionError(recorder, test.err)
			if recorder.Code != test.status {
				t.Fatalf(
					"status=%d want=%d body=%s",
					recorder.Code,
					test.status,
					recorder.Body.String(),
				)
			}
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(
				recorder.Body.Bytes(),
				&response,
			); err != nil {
				t.Fatal(err)
			}
			if response.Error.Code != test.code {
				t.Fatalf(
					"code=%q want=%q body=%s",
					response.Error.Code,
					test.code,
					recorder.Body.String(),
				)
			}
		})
	}
}
