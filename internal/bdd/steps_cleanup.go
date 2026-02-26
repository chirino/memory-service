package bdd

import (
	"context"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		if s.Suite.DB == nil {
			return
		}
		// Clear database before each scenario (matches Java @Before clearDatabase)
		ctx.Before(func(ctx context.Context, sc *godog.Scenario) (context.Context, error) {
			return ctx, s.Suite.DB.ClearAll(ctx)
		})
	})
}
