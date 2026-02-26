package cucumber

import (
	"context"
	"sync"
	"time"

	"github.com/cucumber/godog"
)

var testCaseLock sync.RWMutex

func init() {
	StepModules = append(StepModules, func(ctx *godog.ScenarioContext, s *TestScenario) {
		ctx.Step(`^LOCK-*$`, s.lock)
		ctx.Step(`^UNLOCK-*$`, s.unlock)
		ctx.Step(`^I sleep for (\d+(\.\d+)?) seconds?$`, s.iSleepForSecond)

		ctx.Before(func(ctx context.Context, sc *godog.Scenario) (context.Context, error) {
			testCaseLock.RLock()
			return ctx, nil
		})

		ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
			if s.hasTestCaseLock {
				testCaseLock.Unlock()
			} else {
				testCaseLock.RUnlock()
			}
			return ctx, nil
		})
	})
}

func (s *TestScenario) lock() error {
	if !s.hasTestCaseLock {
		testCaseLock.RUnlock()
		testCaseLock.Lock()
		s.hasTestCaseLock = true
	}
	return nil
}

func (s *TestScenario) unlock() error {
	if s.hasTestCaseLock {
		testCaseLock.Unlock()
		testCaseLock.RLock()
		s.hasTestCaseLock = false
	}
	return nil
}

func (s *TestScenario) iSleepForSecond(seconds float64) error {
	time.Sleep(time.Duration(seconds * float64(time.Second)))
	return nil
}
