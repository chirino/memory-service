package bdd

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		a := &adminStatsSteps{s: s}
		ctx.Step(`^Prometheus is unavailable$`, a.prometheusIsUnavailable)
		ctx.Step(`^Prometheus is available$`, a.prometheusIsAvailable)
		ctx.Step(`^the response should be a time series with metric "([^"]*)"$`, a.theResponseShouldBeATimeSeriesWithMetric)
		ctx.Step(`^the response should be a time series with unit "([^"]*)"$`, a.theResponseShouldBeATimeSeriesWithUnit)
		ctx.Step(`^the response time series data should have at least (\d+) points?$`, a.theResponseTimeSeriesDataShouldHaveAtLeastPoints)
		ctx.Step(`^the response should be a multi-series with metric "([^"]*)"$`, a.theResponseShouldBeAMultiSeriesWithMetric)
		ctx.Step(`^the response should be a multi-series with unit "([^"]*)"$`, a.theResponseShouldBeAMultiSeriesWithUnit)

		// Reset Prometheus to available before each scenario.
		ctx.Before(func(ctx2 context.Context, sc *godog.Scenario) (context.Context, error) {
			if mp := a.mockProm(); mp != nil {
				mp.SetAvailable(true)
			}
			return ctx2, nil
		})
	})
}

type adminStatsSteps struct {
	s *cucumber.TestScenario
}

func (a *adminStatsSteps) mockProm() *MockPrometheus {
	if mp, ok := a.s.Suite.Extra["mockPrometheus"]; ok {
		return mp.(*MockPrometheus)
	}
	return nil
}

func (a *adminStatsSteps) prometheusIsUnavailable() error {
	if mp := a.mockProm(); mp != nil {
		mp.SetAvailable(false)
	}
	return nil
}

func (a *adminStatsSteps) prometheusIsAvailable() error {
	if mp := a.mockProm(); mp != nil {
		mp.SetAvailable(true)
	}
	return nil
}

func (a *adminStatsSteps) theResponseShouldBeATimeSeriesWithMetric(metric string) error {
	session := a.s.Session()
	respJSON, err := session.RespJSON()
	if err != nil {
		return err
	}
	value := jsonPathGet(respJSON, "metric")
	actual := fmt.Sprintf("%v", value)
	if actual != metric {
		return fmt.Errorf("expected time series metric '%s', got '%s'", metric, actual)
	}
	return nil
}

func (a *adminStatsSteps) theResponseShouldBeATimeSeriesWithUnit(unit string) error {
	session := a.s.Session()
	respJSON, err := session.RespJSON()
	if err != nil {
		return err
	}
	value := jsonPathGet(respJSON, "unit")
	actual := fmt.Sprintf("%v", value)
	if actual != unit {
		return fmt.Errorf("expected time series unit '%s', got '%s'", unit, actual)
	}
	return nil
}

func (a *adminStatsSteps) theResponseTimeSeriesDataShouldHaveAtLeastPoints(minCount int) error {
	session := a.s.Session()
	respJSON, err := session.RespJSON()
	if err != nil {
		return err
	}
	data := jsonPathGet(respJSON, "data")
	arr, ok := data.([]interface{})
	if !ok {
		return fmt.Errorf("response 'data' is not an array. Response: %s", string(session.RespBytes))
	}
	if len(arr) < minCount {
		return fmt.Errorf("expected at least %d data points, got %d", minCount, len(arr))
	}
	return nil
}

func (a *adminStatsSteps) theResponseShouldBeAMultiSeriesWithMetric(metric string) error {
	return a.theResponseShouldBeATimeSeriesWithMetric(metric)
}

func (a *adminStatsSteps) theResponseShouldBeAMultiSeriesWithUnit(unit string) error {
	return a.theResponseShouldBeATimeSeriesWithUnit(unit)
}
