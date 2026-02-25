package bdd

import (
	"context"
	"encoding/json"
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
	if sq.s.Suite.DB == nil {
		return fmt.Errorf("no TestDB configured")
	}

	expanded, err := sq.s.Expand(query.Content)
	if err != nil {
		return err
	}

	sq.lastRows, err = sq.s.Suite.DB.ExecSQL(context.Background(), expanded)
	if err != nil {
		return err
	}

	// nil lastRows means "skip SQL" (MongoDB backend) — don't store response
	if sq.lastRows == nil {
		return nil
	}

	// Store result as JSON in response bytes so response assertions work
	result, err := json.Marshal(sq.lastRows)
	if err != nil {
		return err
	}
	session := sq.s.Session()
	session.SetRespBytes(result)

	return nil
}

func (sq *sqlSteps) theSQLResultShouldHaveRows(count int) error {
	if sq.lastRows == nil {
		return nil // skip for non-SQL backends (e.g. MongoDB)
	}
	if len(sq.lastRows) != count {
		return fmt.Errorf("expected %d row(s), got %d", count, len(sq.lastRows))
	}
	return nil
}

func (sq *sqlSteps) theSQLResultShouldMatch(expected *godog.Table) error {
	if sq.lastRows == nil {
		return nil // skip for non-SQL backends (e.g. MongoDB)
	}
	if len(expected.Rows) < 2 {
		return fmt.Errorf("expected table must have a header row and at least one data row")
	}

	// First row is headers
	headers := make([]string, len(expected.Rows[0].Cells))
	for i, cell := range expected.Rows[0].Cells {
		headers[i] = cell.Value
	}

	// Remaining rows are expected data
	for rowIdx := 1; rowIdx < len(expected.Rows); rowIdx++ {
		dataRowIdx := rowIdx - 1
		if dataRowIdx >= len(sq.lastRows) {
			return fmt.Errorf("expected at least %d data row(s), got %d", rowIdx, len(sq.lastRows))
		}
		for colIdx, cell := range expected.Rows[rowIdx].Cells {
			colName := headers[colIdx]
			actualVal := sq.lastRows[dataRowIdx][colName]
			actualStr := fmt.Sprintf("%v", actualVal)
			expectedVal := cell.Value
			// Expand variables in expected value
			expanded, err := sq.s.Expand(expectedVal)
			if err != nil {
				return err
			}
			if actualStr != expanded {
				return fmt.Errorf("SQL result row %d column '%s': expected '%s', got '%s'", dataRowIdx, colName, expanded, actualStr)
			}
		}
	}
	return nil
}

func (sq *sqlSteps) theSQLResultColumnShouldBeNonNull(column string) error {
	if sq.lastRows == nil {
		return nil // skip for non-SQL backends (e.g. MongoDB)
	}
	if len(sq.lastRows) == 0 {
		return fmt.Errorf("SQL result has no rows")
	}
	value, ok := sq.lastRows[0][column]
	if !ok {
		return fmt.Errorf("column '%s' not found in SQL result", column)
	}
	if value == nil {
		return fmt.Errorf("column '%s' is null, expected non-null", column)
	}
	return nil
}

func (sq *sqlSteps) theSQLResultAtRowColumnShouldBe(row int, column, expected string) error {
	if sq.lastRows == nil {
		return nil // skip for non-SQL backends (e.g. MongoDB)
	}
	if row >= len(sq.lastRows) {
		return fmt.Errorf("row index %d out of range (have %d rows)", row, len(sq.lastRows))
	}
	expanded, err := sq.s.Expand(expected)
	if err != nil {
		return err
	}
	value := sq.lastRows[row][column]
	actual := fmt.Sprintf("%v", value)
	if actual != expanded {
		return fmt.Errorf("SQL result row %d column '%s': expected '%s', got '%s'", row, column, expanded, actual)
	}
	return nil
}
