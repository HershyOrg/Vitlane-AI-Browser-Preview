package domain

import (
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type Decision string

const (
	DecisionNoAction       Decision = "NO_ACTION"
	DecisionAddTarget      Decision = "ADD_TARGET"
	DecisionResearchAgain  Decision = "RESEARCH_AGAIN"
	DecisionNeedsSelection Decision = "NEEDS_SELECTION"
)

type Source string

const (
	SourceDeterministic Source = "DETERMINISTIC"
	SourceManaged       Source = "MANAGED"
)

type Operation string

const (
	OperationAdd     Operation = "ADD"
	OperationRefine  Operation = "REFINE"
	OperationUnknown Operation = "UNKNOWN"
)

var (
	ErrInvalid             = errors.New("AUTO_RESEARCH_INVALID")
	ErrVersionConflict     = errors.New("VERSION_CONFLICT")
	ErrIdempotencyConflict = errors.New("AUTO_RESEARCH_IDEMPOTENCY_CONFLICT")
	ErrInProgress          = errors.New("AUTO_RESEARCH_IN_PROGRESS")
	ErrSnapshotChanged     = errors.New("AUTO_RESEARCH_SNAPSHOT_CHANGED")
)

type SemanticRequest struct {
	Operation               Operation `json:"operation"`
	ProductReferenceEnglish string    `json:"productReferenceEnglish"`
	ModifierTermsEnglish    []string  `json:"modifierTermsEnglish"`
	ReferencedOrdinal       *int      `json:"referencedOrdinal"`
}

type Target struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	NormalizedIntent string `json:"normalizedIntent"`
	OrderIndex       int    `json:"orderIndex"`
	SessionID        string `json:"sessionId,omitempty"`
	SessionVersion   int64  `json:"sessionVersion,omitempty"`
	Researchable     bool   `json:"researchable"`
}

type Resolution struct {
	Decision   Decision `json:"decision"`
	Source     Source   `json:"source"`
	TargetID   string   `json:"targetId,omitempty"`
	ReasonCode string   `json:"reasonCode"`
}

// NeedsEnglishNormalization implements the Research language invariant. UI
// locale is irrelevant: any letter outside Unicode Latin sends the original
// text to the server-owned English semantic normalizer; Latin-only input is
// parsed as English without translation.
func NeedsEnglishNormalization(value string) bool {
	for _, current := range value {
		if unicode.IsLetter(current) && !unicode.In(current, unicode.Latin) {
			return true
		}
	}
	return false
}

var ordinalPattern = regexp.MustCompile(`(?i)\b(\d+)(?:st|nd|rd|th)\b`)

var explicitAddPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:add|create|start)(?:\s+[a-z0-9]+){0,6}\s+(?:target|category)\b`),
	regexp.MustCompile(`\b(?:new|separate|another)(?:\s+[a-z0-9]+){0,6}\s+(?:target|category)\b`),
}

// ParseEnglish is intentionally small. It only extracts high-signal operation,
// ordinal and product tokens; everything uncertain is left for the Managed
// enum classifier instead of being guessed in the browser.
func ParseEnglish(value string) SemanticRequest {
	tokens := tokenize(value)
	operation := OperationUnknown
	if explicitAddRequest(value) {
		operation = OperationAdd
	} else if containsAny(tokens,
		"again", "research", "refine", "revisit", "more", "cheaper",
		"price", "option", "options", "shipping", "delivery", "color",
		"colour", "size", "weight", "waterproof", "review", "budget",
		"brand", "feature", "features", "spec", "specification",
	) {
		operation = OperationRefine
	}
	ordinal := englishOrdinal(tokens, strings.ToLower(value))
	reference := make([]string, 0, len(tokens))
	modifiers := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := modifierWords[token]; ok {
			modifiers = append(modifiers, token)
			continue
		}
		if _, ok := ignoredWords[token]; ok || isOrdinalToken(token) {
			continue
		}
		reference = append(reference, token)
	}
	return SemanticRequest{
		Operation: operation, ProductReferenceEnglish: strings.Join(reference, " "),
		ModifierTermsEnglish: unique(modifiers), ReferencedOrdinal: ordinal,
	}
}

func explicitAddRequest(value string) bool {
	normalized := strings.Join(tokenize(value), " ")
	for _, pattern := range explicitAddPatterns {
		if pattern.MatchString(normalized) {
			return true
		}
	}
	return false
}

// ResolveDeterministically returns true only for a conservative, executable
// result. A weak match or a close second place deliberately falls through to
// the Managed enum classifier.
func ResolveDeterministically(
	semantic SemanticRequest,
	targets []Target,
) (Resolution, bool) {
	active := researchableTargets(targets)
	if semantic.Operation == OperationAdd {
		return Resolution{
			Decision: DecisionAddTarget, Source: SourceDeterministic,
			ReasonCode: "EXPLICIT_ADD_REQUEST",
		}, true
	}
	if semantic.Operation != OperationRefine {
		return Resolution{}, false
	}
	if semantic.ReferencedOrdinal != nil {
		for _, target := range active {
			if target.OrderIndex+1 == *semantic.ReferencedOrdinal {
				return researchResolution(target.ID, "EXPLICIT_TARGET_ORDINAL"), true
			}
		}
		return Resolution{}, false
	}
	referenceTokens := tokenize(semantic.ProductReferenceEnglish)
	if len(referenceTokens) == 0 {
		if len(active) == 1 {
			return researchResolution(active[0].ID, "ONLY_RESEARCHABLE_TARGET"), true
		}
		return Resolution{}, false
	}
	type scored struct {
		id    string
		score float64
	}
	ranking := make([]scored, 0, len(active))
	for _, target := range active {
		ranking = append(ranking, scored{
			id: target.ID, score: matchScore(referenceTokens, tokenize(target.NormalizedIntent)),
		})
	}
	sort.SliceStable(ranking, func(i, j int) bool { return ranking[i].score > ranking[j].score })
	if len(ranking) == 0 || ranking[0].score < .84 {
		return Resolution{}, false
	}
	if len(ranking) > 1 && ranking[0].score-ranking[1].score < .24 {
		return Resolution{}, false
	}
	return researchResolution(ranking[0].id, "UNIQUE_NORMALIZED_TARGET_MATCH"), true
}

func researchResolution(targetID, reason string) Resolution {
	return Resolution{
		Decision: DecisionResearchAgain, Source: SourceDeterministic,
		TargetID: targetID, ReasonCode: reason,
	}
}

func matchScore(reference, candidate []string) float64 {
	if len(reference) == 0 || len(candidate) == 0 {
		return 0
	}
	matched := 0
	for _, wanted := range reference {
		for _, actual := range candidate {
			if wanted == actual || len(wanted) >= 4 && len(actual) >= 4 &&
				levenshteinAtMostOne(wanted, actual) {
				matched++
				break
			}
		}
	}
	coverage := float64(matched) / float64(len(reference))
	precision := float64(matched) / float64(len(candidate))
	return math.Min(1, coverage*.88+precision*.12)
}

func tokenize(value string) []string {
	var builder strings.Builder
	for _, current := range strings.ToLower(strings.TrimSpace(value)) {
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' {
			builder.WriteRune(current)
		} else {
			builder.WriteByte(' ')
		}
	}
	fields := strings.Fields(builder.String())
	for index, field := range fields {
		if len(field) > 4 && strings.HasSuffix(field, "s") {
			fields[index] = strings.TrimSuffix(field, "s")
		}
	}
	return unique(fields)
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func researchableTargets(targets []Target) []Target {
	result := make([]Target, 0, len(targets))
	for _, target := range targets {
		if target.Researchable {
			result = append(result, target)
		}
	}
	return result
}

func containsAny(tokens []string, values ...string) bool {
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		set[token] = struct{}{}
	}
	for _, value := range values {
		if _, ok := set[value]; ok {
			return true
		}
	}
	return false
}

func englishOrdinal(tokens []string, original string) *int {
	wordOrdinals := map[string]int{
		"first": 1, "second": 2, "third": 3, "fourth": 4, "fifth": 5,
	}
	for _, token := range tokens {
		if value, ok := wordOrdinals[token]; ok {
			return &value
		}
	}
	match := ordinalPattern.FindStringSubmatch(original)
	if len(match) == 2 {
		if value, err := strconv.Atoi(match[1]); err == nil && value > 0 && value <= 100 {
			return &value
		}
	}
	return nil
}

func isOrdinalToken(value string) bool {
	if _, ok := map[string]struct{}{
		"first": {}, "second": {}, "third": {}, "fourth": {}, "fifth": {},
	}[value]; ok {
		return true
	}
	return ordinalPattern.MatchString(value)
}

func levenshteinAtMostOne(left, right string) bool {
	if left == right {
		return true
	}
	if delta := len(left) - len(right); delta < -1 || delta > 1 {
		return false
	}
	i, j, edits := 0, 0, 0
	for i < len(left) && j < len(right) {
		if left[i] == right[j] {
			i++
			j++
			continue
		}
		edits++
		if edits > 1 {
			return false
		}
		switch {
		case len(left) > len(right):
			i++
		case len(right) > len(left):
			j++
		default:
			i++
			j++
		}
	}
	if i < len(left) || j < len(right) {
		edits++
	}
	return edits <= 1
}

var modifierWords = map[string]struct{}{
	"cheaper": {}, "price": {}, "color": {}, "colour": {}, "material": {},
	"shipping": {}, "delivery": {}, "size": {}, "weight": {}, "waterproof": {},
	"budget": {}, "brand": {}, "feature": {}, "features": {}, "spec": {},
	"specification": {}, "option": {}, "options": {}, "more": {},
}

var ignoredWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "another": {}, "again": {}, "add": {},
	"can": {}, "could": {}, "create": {}, "find": {}, "for": {}, "include": {},
	"it": {}, "me": {}, "new": {}, "of": {}, "one": {}, "please": {}, "product": {},
	"refine": {}, "research": {}, "revisit": {}, "separate": {}, "some": {}, "target": {},
	"the": {}, "this": {}, "to": {}, "with": {}, "would": {}, "you": {},
}
