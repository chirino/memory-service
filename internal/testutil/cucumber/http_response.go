package cucumber

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"github.com/itchyny/gojq"
	"github.com/pmezard/go-difflib/difflib"
)

func init() {
	StepModules = append(StepModules, func(ctx *godog.ScenarioContext, s *TestScenario) {
		ctx.Step(`^the response code should be (\d+)$`, s.theResponseCodeShouldBe)
		ctx.Step(`^the response should match json:$`, s.TheResponseShouldMatchJSONDoc)
		ctx.Step(`^the response should contain json:$`, s.TheResponseShouldContainJSONDoc)
		ctx.Step(`^the response should contain "([^"]*)"$`, s.theResponseShouldContain)
		ctx.Step(`^the response should match:$`, s.theResponseShouldMatchTextDoc)
		ctx.Step(`^the response should match "([^"]*)"$`, s.theResponseShouldMatchText)
		ctx.Step(`^I store the "([^"]*)" selection from the response as \${([^}]*)}$`, s.iStoreTheSelectionFromTheResponseAs)
		ctx.Step(`^I store the \${([^}]*)} as \${([^}]*)}$`, s.iStoreVariableAsVariable)
		ctx.Step(`^I delete the \${([^}]*)} "([^"]*)" key$`, s.iDeleteTheMapKey)
		ctx.Step(`^the "(.*)" selection from the response should match "([^"]*)"$`, s.theSelectionFromTheResponseShouldMatch)
		ctx.Step(`^the response header "([^"]*)" should match "([^"]*)"$`, s.theResponseHeaderShouldMatch)
		ctx.Step(`^the response header "([^"]*)" should start with "([^"]*)"$`, s.theResponseHeaderShouldStartWith)
		ctx.Step(`^the "([^"]*)" selection from the response should match json:$`, s.theSelectionFromTheResponseShouldMatchJSON)
		ctx.Step(`^\${([^}]*)} is not empty$`, s.variableIsNotEmpty)
		ctx.Step(`^"([^"]*)" should match "([^"]*)"$`, s.textShouldMatchText)
		ctx.Step(`^\${([^}]*)} should match:$`, s.theVariableShouldMatchText)
		ctx.Step(`^\${([^}]*)} should contain json:$`, s.theVariableShouldContainJSON)
	})
}

func (s *TestScenario) variableIsNotEmpty(name string) error {
	value, err := s.Resolve(name)
	if err != nil {
		return err
	}
	if value == nil || value == "" {
		return fmt.Errorf("variable ${%s} is empty", name)
	}
	return nil
}

func (s *TestScenario) theResponseCodeShouldBe(expected int) error {
	session := s.Session()
	if session.Resp == nil {
		return fmt.Errorf("no HTTP response available")
	}
	actual := session.Resp.StatusCode
	if expected != actual {
		return fmt.Errorf("expected response code to be: %d, but actual is: %d, body: %s", expected, actual, string(session.RespBytes))
	}
	return nil
}

func (s *TestScenario) TheResponseShouldMatchJSONDoc(expected *godog.DocString) error {
	return s.theResponseShouldMatchJSON(expected.Content)
}

func (s *TestScenario) theResponseShouldMatchJSON(expected string) error {
	session := s.Session()
	if len(session.RespBytes) == 0 {
		return fmt.Errorf("got an empty response from server, expected a json body")
	}
	return s.JSONMustMatch(string(session.RespBytes), expected, true)
}

func (s *TestScenario) TheResponseShouldContainJSONDoc(expected *godog.DocString) error {
	return s.theResponseShouldContainJSON(expected.Content)
}

func (s *TestScenario) theResponseShouldContainJSON(expected string) error {
	session := s.Session()
	if len(session.RespBytes) == 0 {
		return fmt.Errorf("got an empty response from server, expected a json body")
	}
	return s.JSONMustContain(string(session.RespBytes), expected, true)
}

func (s *TestScenario) theVariableShouldContainJSON(variableName string, expected *godog.DocString) error {
	expanded, err := s.Expand(fmt.Sprintf("${%s}", variableName))
	if err != nil {
		return err
	}
	return s.JSONMustContain(expanded, expected.Content, true)
}

func (s *TestScenario) theResponseShouldContain(expected string) error {
	session := s.Session()
	responseBody := string(session.RespBytes)
	if !strings.Contains(responseBody, expected) {
		return fmt.Errorf("expected response to contain '%s', but it does not. Response body: %s", expected, responseBody)
	}
	return nil
}

func (s *TestScenario) textShouldMatchText(actual, expected string) error {
	expanded, err := s.Expand(expected)
	if err != nil {
		return err
	}
	actual, err = s.Expand(actual)
	if err != nil {
		return err
	}
	if expanded != actual {
		diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A:        difflib.SplitLines(expanded),
			B:        difflib.SplitLines(actual),
			FromFile: "Expected",
			ToFile:   "Actual",
			Context:  1,
		})
		return fmt.Errorf("actual does not match expected, diff:\n%s", diff)
	}
	return nil
}

func (s *TestScenario) theVariableShouldMatchText(variable string, expected *godog.DocString) error {
	expanded, err := s.Expand(expected.Content)
	if err != nil {
		return err
	}
	actual, err := s.ResolveString(variable)
	if err != nil {
		return err
	}
	if expanded != actual {
		diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A:        difflib.SplitLines(expanded),
			B:        difflib.SplitLines(actual),
			FromFile: "Expected",
			ToFile:   "Actual",
			Context:  1,
		})
		return fmt.Errorf("actual does not match expected, diff:\n%s", diff)
	}
	return nil
}

func (s *TestScenario) theResponseShouldMatchTextDoc(expected *godog.DocString) error {
	return s.theResponseShouldMatchText(expected.Content)
}

func (s *TestScenario) theResponseShouldMatchText(expected string) error {
	session := s.Session()
	expanded, err := s.Expand(expected)
	if err != nil {
		return err
	}
	actual := string(session.RespBytes)
	if expanded != actual {
		diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A:        difflib.SplitLines(expanded),
			B:        difflib.SplitLines(actual),
			FromFile: "Expected",
			ToFile:   "Actual",
			Context:  1,
		})
		return fmt.Errorf("actual does not match expected, diff:\n%s", diff)
	}
	return nil
}

func (s *TestScenario) theResponseHeaderShouldMatch(header, expected string) error {
	session := s.Session()
	expanded, err := s.Expand(expected)
	if err != nil {
		return err
	}
	actual := session.Resp.Header.Get(header)
	if expanded != actual {
		return fmt.Errorf("response header '%s' does not match expected: %v, actual: %v, body:\n%s", header, expanded, actual, string(session.RespBytes))
	}
	return nil
}

func (s *TestScenario) theResponseHeaderShouldStartWith(header, prefix string) error {
	session := s.Session()
	prefix, err := s.Expand(prefix)
	if err != nil {
		return err
	}
	actual := session.Resp.Header.Get(header)
	if !strings.HasPrefix(actual, prefix) {
		return fmt.Errorf("response header '%s' does not start with prefix: %v, actual: %v, body:\n%s", header, prefix, actual, string(session.RespBytes))
	}
	return nil
}

func (s *TestScenario) iStoreVariableAsVariable(name, as string) error {
	value, err := s.Resolve(name)
	if err != nil {
		return err
	}
	s.Variables[as] = value
	return nil
}

func (s *TestScenario) iDeleteTheMapKey(mapName, key string) error {
	_, err := s.Resolve(mapName)
	if err != nil {
		return err
	}
	// For simplicity, just delete the key from the Variables map if the mapName matches
	delete(s.Variables, key)
	return nil
}

func (s *TestScenario) iStoreTheSelectionFromTheResponseAs(selector, as string) error {
	session := s.Session()
	doc, err := session.RespJSON()
	if err != nil {
		return err
	}

	query, err := gojq.Parse(selector)
	if err != nil {
		return err
	}

	iter := query.Run(doc)
	if next, found := iter.Next(); found {
		s.Variables[as] = next
		return nil
	}
	return fmt.Errorf("expected JSON does not have node that matches selector: %s", selector)
}

func (s *TestScenario) theSelectionFromTheResponseShouldMatch(selector, expected string) error {
	session := s.Session()
	doc, err := session.RespJSON()
	if err != nil {
		return err
	}

	query, err := gojq.Parse(selector)
	if err != nil {
		return err
	}

	expected, err = s.Expand(expected)
	if err != nil {
		return err
	}

	iter := query.Run(doc)
	if actual, found := iter.Next(); found {
		if actual == nil {
			actual = "null"
		} else {
			actual = fmt.Sprintf("%v", actual)
		}
		if actual != expected {
			return fmt.Errorf("selected JSON does not match. expected: %v, actual: %v", expected, actual)
		}
		return nil
	}
	return fmt.Errorf("expected JSON does not have node that matches selector: %s", selector)
}

func (s *TestScenario) theSelectionFromTheResponseShouldMatchJSON(selector string, expected *godog.DocString) error {
	session := s.Session()
	doc, err := session.RespJSON()
	if err != nil {
		return err
	}

	query, err := gojq.Parse(selector)
	if err != nil {
		return err
	}

	iter := query.Run(doc)
	if actual, found := iter.Next(); found {
		actual, err := json.Marshal(actual)
		if err != nil {
			return err
		}
		return s.JSONMustMatch(string(actual), expected.Content, true)
	}
	return fmt.Errorf("expected JSON does not have node that matches selector: %s", selector)
}
