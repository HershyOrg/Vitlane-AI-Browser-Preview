package domain

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

type actionParserGoldenFixture struct {
	Schema  string                    `json:"schema"`
	Catalog []actionParserGoldenEntry `json:"catalog"`
	Cases   []actionParserGoldenCase  `json:"cases"`
}

type actionParserGoldenEntry struct {
	Type        string `json:"type"`
	Alias       string `json:"alias"`
	SubjectType string `json:"subjectType"`
	BodySchema  string `json:"bodySchema"`
}

type actionParserGoldenCase struct {
	Name      string                       `json:"name"`
	Command   string                       `json:"command"`
	Valid     bool                         `json:"valid"`
	Parsed    *ParsedCurationActionCommand `json:"parsed"`
	ErrorCode string                       `json:"errorCode"`
}

func TestCurationActionCommandParserMatchesSharedGolden(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile(
		"../../../../shared/openapi/fixtures/" +
			"curation-action-command-parser.v1.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	var fixture actionParserGoldenFixture
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "CurationActionCommandParserFixture.v1" {
		t.Fatalf("unexpected fixture schema %q", fixture.Schema)
	}
	if len(fixture.Catalog) != len(curationActionDefinitions) {
		t.Fatalf(
			"catalog entries=%d definitions=%d",
			len(fixture.Catalog),
			len(curationActionDefinitions),
		)
	}
	for index, expected := range fixture.Catalog {
		actual := curationActionDefinitions[index]
		if string(actual.id) != expected.Type ||
			actual.alias != expected.Alias ||
			string(actual.subjectType) != expected.SubjectType ||
			string(actual.bodySchema) != expected.BodySchema {
			t.Fatalf(
				"catalog[%d]=%#v want=%#v",
				index,
				actual,
				expected,
			)
		}
	}

	for _, vector := range fixture.Cases {
		vector := vector
		t.Run(vector.Name, func(t *testing.T) {
			actual, err := ParseCurationActionCommand(vector.Command)
			if vector.Valid {
				if err != nil {
					t.Fatal(err)
				}
				if vector.Parsed == nil ||
					!reflect.DeepEqual(actual, *vector.Parsed) {
					t.Fatalf("parsed=%#v want=%#v", actual, vector.Parsed)
				}
				return
			}
			if err == nil {
				t.Fatalf("invalid command parsed as %#v", actual)
			}
			if got := curationActionErrorCode(err); got != vector.ErrorCode {
				t.Fatalf("error=%v code=%q want=%q", err, got, vector.ErrorCode)
			}
		})
	}
}

func curationActionErrorCode(err error) string {
	for _, candidate := range []error{
		ErrCurationActionCommandInvalid,
		ErrCurationActionAliasUnknown,
		ErrCurationActionBodyInvalid,
	} {
		if errors.Is(err, candidate) {
			return candidate.Error()
		}
	}
	return ""
}
