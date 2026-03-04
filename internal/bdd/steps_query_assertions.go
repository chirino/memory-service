package bdd

import (
	"encoding/json"
	"fmt"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func storeQueryRowsAsResponse(s *cucumber.TestScenario, rows []map[string]interface{}) error {
	// nil rows means "skip query assertions" on this backend.
	if rows == nil {
		return nil
	}

	result, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	s.Session().SetRespBytes(result)
	return nil
}

func assertQueryResultHasRows(rows []map[string]interface{}, count int, resultType string) error {
	if rows == nil {
		return nil
	}
	if len(rows) != count {
		return fmt.Errorf("expected %d row(s), got %d", count, len(rows))
	}
	return nil
}

func assertQueryResultMatches(rows []map[string]interface{}, expected *godog.Table, s *cucumber.TestScenario, resultType string) error {
	if rows == nil {
		return nil
	}
	if len(expected.Rows) < 2 {
		return fmt.Errorf("expected table must have a header row and at least one data row")
	}

	headers := make([]string, len(expected.Rows[0].Cells))
	for i, cell := range expected.Rows[0].Cells {
		headers[i] = cell.Value
	}

	for rowIdx := 1; rowIdx < len(expected.Rows); rowIdx++ {
		dataRowIdx := rowIdx - 1
		if dataRowIdx >= len(rows) {
			return fmt.Errorf("expected at least %d data row(s), got %d", rowIdx, len(rows))
		}
		for colIdx, cell := range expected.Rows[rowIdx].Cells {
			colName := headers[colIdx]
			actualVal := rows[dataRowIdx][colName]
			actualStr := fmt.Sprintf("%v", actualVal)

			expanded, err := s.Expand(cell.Value)
			if err != nil {
				return err
			}
			if actualStr != expanded {
				return fmt.Errorf("%s result row %d column '%s': expected '%s', got '%s'",
					resultType, dataRowIdx, colName, expanded, actualStr)
			}
		}
	}
	return nil
}

func assertQueryResultColumnNonNull(rows []map[string]interface{}, column, resultType string) error {
	if rows == nil {
		return nil
	}
	if len(rows) == 0 {
		return fmt.Errorf("%s result has no rows", resultType)
	}
	value, ok := rows[0][column]
	if !ok {
		return fmt.Errorf("column '%s' not found in %s result", column, resultType)
	}
	if value == nil {
		return fmt.Errorf("column '%s' is null, expected non-null", column)
	}
	return nil
}

func assertQueryResultAtRowColumnShouldBe(rows []map[string]interface{}, row int, column, expected, resultType string, s *cucumber.TestScenario) error {
	if rows == nil {
		return nil
	}
	if row >= len(rows) {
		return fmt.Errorf("row index %d out of range (have %d rows)", row, len(rows))
	}
	expanded, err := s.Expand(expected)
	if err != nil {
		return err
	}
	value := rows[row][column]
	actual := fmt.Sprintf("%v", value)
	if actual != expanded {
		return fmt.Errorf("%s result row %d column '%s': expected '%s', got '%s'",
			resultType, row, column, expanded, actual)
	}
	return nil
}
