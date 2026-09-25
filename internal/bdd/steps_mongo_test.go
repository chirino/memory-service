package bdd

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

// newUnitTestScenario runs a one-step in-memory feature so the cucumber package builds a
// fully initialized TestScenario (its session map is unexported), then returns it.
func newUnitTestScenario(t *testing.T) *cucumber.TestScenario {
	t.Helper()

	var captured *cucumber.TestScenario
	orig := cucumber.StepModules
	cucumber.StepModules = append(slices.Clone(orig), func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		ctx.Step(`^the scenario is captured$`, func() error {
			captured = s
			return nil
		})
	})
	t.Cleanup(func() { cucumber.StepModules = orig })

	suite := cucumber.NewTestSuite()
	status := godog.TestSuite{
		Name:                "mongo-doc-string-expansion",
		ScenarioInitializer: suite.InitializeScenario,
		Options: &godog.Options{
			Format:      "progress",
			Output:      io.Discard,
			Strict:      true,
			Concurrency: 1,
			FeatureContents: []godog.Feature{{
				Name:     "capture.feature",
				Contents: []byte("Feature: capture\n  Scenario: capture\n    Given the scenario is captured\n"),
			}},
		},
	}.Run()
	if status != 0 || captured == nil {
		t.Fatalf("failed to initialize test scenario (status %d)", status)
	}
	return captured
}

// Mongo query doc strings are full of `$` operators and field paths; only `${var}`
// references may be expanded.
func TestExpandMongoDocStringKeepsOperatorsAndFieldPaths(t *testing.T) {
	s := newUnitTestScenario(t)
	s.Variables["conversationId"] = "c-123"

	input := `[
  {"$match": {"conversation_id": "${conversationId}", "status": {"$ne": "done"}}},
  {"$project": {"path": "$task_body.path"}}
]`
	got, err := expandMongoDocString(s, input)
	if err != nil {
		t.Fatalf("expandMongoDocString: %v", err)
	}

	want := `[
  {"$match": {"conversation_id": "c-123", "status": {"$ne": "done"}}},
  {"$project": {"path": "$task_body.path"}}
]`
	if got != want {
		t.Fatalf("unexpected expansion:\n got: %s\nwant: %s", got, want)
	}
}

func TestExpandMongoDocStringUndefinedVariable(t *testing.T) {
	s := newUnitTestScenario(t)

	_, err := expandMongoDocString(s, `{"$match": {"id": "${missing}"}}`)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected undefined variable error, got %v", err)
	}
}
