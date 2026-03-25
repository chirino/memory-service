package bdd

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		sq := &sqlSteps{s: s}
		ctx.Step(`^I execute SQL query:$`, sq.iExecuteSQLQuery)
		ctx.Step(`^the SQL result should have (\d+) rows?$`, sq.theSQLResultShouldHaveRows)
		ctx.Step(`^the SQL result should match:$`, sq.theSQLResultShouldMatch)
		ctx.Step(`^the SQL result column "([^"]*)" should be non-null$`, sq.theSQLResultColumnShouldBeNonNull)
		ctx.Step(`^the SQL result at row (\d+) column "([^"]*)" should be "([^"]*)"$`, sq.theSQLResultAtRowColumnShouldBe)
	})
}

type sqlSteps struct {
	s        *cucumber.TestScenario
	lastRows []map[string]interface{}
}

func (sq *sqlSteps) iExecuteSQLQuery(query *godog.DocString) error {
	if sq.s.TestDB() == nil {
		return fmt.Errorf("no TestDB configured")
	}

	expanded, err := sq.s.Expand(query.Content)
	if err != nil {
		return err
	}
	expanded = sq.s.RewriteQuotedUsers(expanded)

	sq.lastRows, err = sq.s.TestDB().ExecSQL(context.Background(), expanded)
	if err != nil {
		return err
	}
	sq.lastRows = sq.s.FilterQueryRows(sq.lastRows)
	if normalized, ok := sq.s.NormalizeValue(sq.lastRows).([]map[string]interface{}); ok {
		sq.lastRows = normalized
	}

	// nil lastRows means "skip SQL" (MongoDB backend) — don't store response
	return storeQueryRowsAsResponse(sq.s, sq.lastRows)
}

func (sq *sqlSteps) theSQLResultShouldHaveRows(count int) error {
	return assertQueryResultHasRows(sq.lastRows, count, "SQL")
}

func (sq *sqlSteps) theSQLResultShouldMatch(expected *godog.Table) error {
	return assertQueryResultMatches(sq.lastRows, expected, sq.s, "SQL")
}

func (sq *sqlSteps) theSQLResultColumnShouldBeNonNull(column string) error {
	return assertQueryResultColumnNonNull(sq.lastRows, column, "SQL")
}

func (sq *sqlSteps) theSQLResultAtRowColumnShouldBe(row int, column, expected string) error {
	return assertQueryResultAtRowColumnShouldBe(sq.lastRows, row, column, expected, "SQL", sq.s)
}
