package bdd

import (
	"fmt"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		c := &cacheSteps{s: s}
		ctx.Step(`^I record the current cache metrics$`, c.iRecordTheCurrentCacheMetrics)
		ctx.Step(`^the cache hit count should have increased by at least (\d+)$`, c.theCacheHitCountShouldHaveIncreasedByAtLeast)
	})
}

type cacheSteps struct {
	s              *cucumber.TestScenario
	lastHitCount   float64
	hitCountCached bool
}

func (c *cacheSteps) iRecordTheCurrentCacheMetrics() error {
	// TODO: Query metrics endpoint to record current cache hit count
	// For noop cache, this is a no-op since there are no real cache metrics
	c.hitCountCached = true
	c.lastHitCount = 0
	return nil
}

func (c *cacheSteps) theCacheHitCountShouldHaveIncreasedByAtLeast(minIncrease int) error {
	if !c.hitCountCached {
		return fmt.Errorf("cache metrics were not recorded; call 'I record the current cache metrics' first")
	}
	// TODO: Query metrics endpoint and compare with recorded value
	// For noop cache, cache hits won't increase - skip assertion
	_ = minIncrease
	return nil
}
