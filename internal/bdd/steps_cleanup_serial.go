package bdd

import (
	"context"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		ctx.Before(func(ctx2 context.Context, sc *godog.Scenario) (context.Context, error) {
			if s.Suite.DB == nil || !isSerialFeature(sc.Uri) {
				return ctx2, nil
			}
			if err := s.Suite.DB.ClearAll(ctx2); err != nil {
				return ctx2, err
			}
			return ctx2, nil
		})
	})
}
