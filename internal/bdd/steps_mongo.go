package bdd

import (
	"context"
	"fmt"
	"regexp"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		mq := &mongoSteps{s: s}
		ctx.Step(`^I execute MongoDB query:$`, mq.iExecuteMongoDBQuery)
		ctx.Step(`^the MongoDB result should have (\d+) rows?$`, mq.theMongoDBResultShouldHaveRows)
		ctx.Step(`^the MongoDB result should match:$`, mq.theMongoDBResultShouldMatch)
		ctx.Step(`^the MongoDB result column "([^"]*)" should be non-null$`, mq.theMongoDBResultColumnShouldBeNonNull)
		ctx.Step(`^the MongoDB result at row (\d+) column "([^"]*)" should be "([^"]*)"$`, mq.theMongoDBResultAtRowColumnShouldBe)
	})
}

type mongoSteps struct {
	s        *cucumber.TestScenario
	lastRows []map[string]interface{}
}

func (mq *mongoSteps) iExecuteMongoDBQuery(query *godog.DocString) error {
	if mq.s.TestDB() == nil {
		return fmt.Errorf("no TestDB configured")
	}

	expanded, err := expandMongoDocString(mq.s, query.Content)
	if err != nil {
		return err
	}
	expanded = mq.s.RewriteQuotedUsers(expanded)

	mq.lastRows, err = mq.s.TestDB().ExecMongoQuery(context.Background(), expanded)
	if err != nil {
		return err
	}
	mq.lastRows = mq.s.FilterQueryRows(mq.lastRows)
	if normalized, ok := mq.s.NormalizeValue(mq.lastRows).([]map[string]interface{}); ok {
		mq.lastRows = normalized
	}

	return storeQueryRowsAsResponse(mq.s, mq.lastRows)
}

func (mq *mongoSteps) theMongoDBResultShouldHaveRows(count int) error {
	return assertQueryResultHasRows(mq.lastRows, count, "MongoDB")
}

func (mq *mongoSteps) theMongoDBResultShouldMatch(expected *godog.Table) error {
	return assertQueryResultMatches(mq.lastRows, expected, mq.s, "MongoDB")
}

func (mq *mongoSteps) theMongoDBResultColumnShouldBeNonNull(column string) error {
	return assertQueryResultColumnNonNull(mq.lastRows, column, "MongoDB")
}

func (mq *mongoSteps) theMongoDBResultAtRowColumnShouldBe(row int, column, expected string) error {
	return assertQueryResultAtRowColumnShouldBe(mq.lastRows, row, column, expected, "MongoDB", mq.s)
}

var mongoTemplateVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func expandMongoDocString(s *cucumber.TestScenario, input string) (string, error) {
	var resolveErr error
	out := mongoTemplateVarPattern.ReplaceAllStringFunc(input, func(match string) string {
		if resolveErr != nil {
			return ""
		}
		name := match[2 : len(match)-1]
		resolved, err := s.ResolveString(name)
		if err != nil {
			resolveErr = err
			return ""
		}
		return resolved
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	return out, nil
}
