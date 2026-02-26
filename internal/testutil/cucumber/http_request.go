package cucumber

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

func init() {
	StepModules = append(StepModules, func(ctx *godog.ScenarioContext, s *TestScenario) {
		// Generic HTTP steps
		ctx.Step(`^the path prefix is "([^"]*)"$`, s.theAPIPrefixIs)
		ctx.Step(`^I (GET|POST|PUT|DELETE|PATCH|OPTION) path "([^"]*)"$`, s.sendHTTPRequest)
		ctx.Step(`^I (GET|POST|PUT|DELETE|PATCH|OPTION) path "([^"]*)" as a json event stream$`, s.sendHTTPRequestAsEventStream)
		ctx.Step(`^I (GET|POST|PUT|DELETE|PATCH|OPTION) path "([^"]*)" with json body:$`, s.SendHTTPRequestWithJSONBody)
		ctx.Step(`^I (GET|POST|PUT|DELETE|PATCH|OPTION) path "([^"]*)" with json body expecting a json event stream:$`, s.SendHTTPRequestWithJSONBodyAsEventStream)
		ctx.Step(`^I wait up to "([^"]*)" seconds for a GET on path "([^"]*)" response "([^"]*)" selection to match "([^"]*)"$`, s.iWaitUpToSecondsForAGETOnPathResponseSelectionToMatch)
		ctx.Step(`^I wait up to "([^"]*)" seconds for a GET on path "([^"]*)" response code to match "([^"]*)"$`, s.iWaitUpToSecondsForAGETOnPathResponseCodeToMatch)
		ctx.Step(`^I wait up to "([^"]*)" seconds for a response event$`, s.iWaitUpToSecondsForAResponseJSONEvent)
		ctx.Step(`^I wait up to "([^"]*)" seconds for a GET on path "([^"]*)" to respond with json:$`, s.iWaitUpToSecondsForAGETOnPathToRespondWithJSON)

		// Generic HTTP steps (I call METHOD "path")
		ctx.Step(`^I call (GET|POST|PUT|DELETE|PATCH) "([^"]*)"$`, s.sendHTTPRequest)
		ctx.Step(`^I call (GET|POST|PUT|DELETE|PATCH) "([^"]*)" with body:$`, s.SendHTTPRequestWithJSONBody)
		ctx.Step(`^I call (GET|POST|PUT|DELETE|PATCH) "([^"]*)" with query "([^"]*)"$`, s.iCallWithQuery)
		ctx.Step(`^I call (POST) "([^"]*)" with Accept "([^"]*)" and body:$`, s.iCallWithAcceptAndBody)
		ctx.Step(`^I call (GET) "([^"]*)" expecting binary$`, s.iCallExpectingBinary)
		ctx.Step(`^I call (GET) "([^"]*)" expecting binary with header "([^"]*)" = "(.*)"$`, s.iCallExpectingBinaryWithHeader)
		ctx.Step(`^I call (GET) "([^"]*)" expecting binary without authentication$`, s.iCallExpectingBinaryNoAuth)
		ctx.Step(`^I call (GET) "([^"]*)" expecting binary without authentication with header "([^"]*)" = "(.*)"$`, s.iCallExpectingBinaryNoAuthWithHeader)

		// Header setting
		ctx.Step(`^I set the "([^"]*)" header to "([^"]*)"$`, s.iSetTheHeaderTo)
	})
}

func (s *TestScenario) theAPIPrefixIs(prefix string) error {
	s.PathPrefix = prefix
	return nil
}

func (s *TestScenario) sendHTTPRequest(method, path string) error {
	return s.SendHTTPRequestWithJSONBody(method, path, nil)
}

func (s *TestScenario) sendHTTPRequestAsEventStream(method, path string) error {
	return s.SendHTTPRequestWithJSONBodyAndStyle(method, path, nil, true, true)
}

func (s *TestScenario) SendHTTPRequestWithJSONBody(method, path string, jsonTxt *godog.DocString) error {
	return s.SendHTTPRequestWithJSONBodyAndStyle(method, path, jsonTxt, false, true)
}

func (s *TestScenario) SendHTTPRequestWithJSONBodyAsEventStream(method, path string, jsonTxt *godog.DocString) error {
	return s.SendHTTPRequestWithJSONBodyAndStyle(method, path, jsonTxt, true, true)
}

func (s *TestScenario) SendHTTPRequestWithJSONBodyAndStyle(method, path string, jsonTxt *godog.DocString, eventStream bool, expandJSON bool) (err error) {
	defer func() {
		switch t := recover().(type) {
		case string:
			err = errors.New(t)
		case error:
			err = t
		}
	}()

	session := s.Session()

	body := &bytes.Buffer{}
	if jsonTxt != nil {
		expanded := jsonTxt.Content
		if expandJSON {
			expanded, err = s.Expand(expanded)
			if err != nil {
				return err
			}
		}
		body.WriteString(expanded)
	}

	expandedPath, err := s.Expand(path)
	if err != nil {
		return err
	}

	fullURL := ""
	expandedPathURL, err := url.Parse(expandedPath)
	if err == nil && expandedPathURL.Scheme != "" {
		fullURL = expandedPath
	} else {
		fullURL = s.Suite.APIURL + s.PathPrefix + expandedPath
	}

	// Reset response state
	if session.Resp != nil {
		_ = session.Resp.Body.Close()
	}
	session.EventStream = false
	session.Resp = nil
	session.RespBytes = nil
	session.respJSON = nil

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return err
	}

	// Consume session headers on every request except Authorization
	req.Header = session.Header
	session.Header = http.Header{}

	if req.Header.Get("Authorization") != "" {
		session.Header.Set("Authorization", req.Header.Get("Authorization"))
	} else if session.TestUser != nil && session.TestUser.Subject != "" {
		req.Header.Set("Authorization", "Bearer "+session.TestUser.Subject)
	}

	// Preserve sticky headers across requests.
	if clientID := req.Header.Get("X-Client-ID"); clientID != "" {
		session.Header.Set("X-Client-ID", clientID)
	}

	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := session.Client.Do(req)
	if err != nil {
		return err
	}

	session.Resp = resp
	session.EventStream = eventStream
	if !eventStream {
		defer func() {
			_ = resp.Body.Close()
		}()
		session.RespBytes, err = io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
	} else {
		c := make(chan interface{})
		session.EventStreamEvents = c
		go func() {
			d := json.NewDecoder(session.Resp.Body)
			defer func() {
				_ = resp.Body.Close()
			}()
			for {
				var event interface{}
				err := d.Decode(&event)
				if err != nil {
					close(c)
					return
				}
				c <- event
			}
		}()
	}

	return nil
}

func (s *TestScenario) iCallWithQuery(method, path, queryString string) error {
	expandedQuery, err := s.Expand(queryString)
	if err != nil {
		return err
	}
	if strings.Contains(path, "?") {
		path = path + "&" + expandedQuery
	} else {
		path = path + "?" + expandedQuery
	}
	return s.sendHTTPRequest(method, path)
}

func (s *TestScenario) iCallWithAcceptAndBody(method, path, accept string, jsonTxt *godog.DocString) error {
	session := s.Session()
	session.Header.Set("Accept", accept)
	return s.SendHTTPRequestWithJSONBody(method, path, jsonTxt)
}

func (s *TestScenario) iCallExpectingBinary(method, path string) error {
	session := s.Session()
	session.Header.Set("Accept", "*/*")
	return s.sendHTTPRequest(method, path)
}

func (s *TestScenario) iCallExpectingBinaryWithHeader(method, path, headerName, headerValue string) error {
	session := s.Session()
	expanded, err := s.Expand(headerValue)
	if err != nil {
		return err
	}
	expanded = strings.ReplaceAll(expanded, `\"`, `"`)
	session.Header.Set(headerName, expanded)
	session.Header.Set("Accept", "*/*")
	return s.sendHTTPRequest(method, path)
}

func (s *TestScenario) iCallExpectingBinaryNoAuth(method, path string) error {
	session := s.Session()
	session.Header.Del("Authorization")
	session.Header.Set("Accept", "*/*")
	// Temporarily unset the user so no Authorization header is sent
	savedUser := session.TestUser
	session.TestUser = nil
	err := s.sendHTTPRequest(method, path)
	session.TestUser = savedUser
	return err
}

func (s *TestScenario) iCallExpectingBinaryNoAuthWithHeader(method, path, headerName, headerValue string) error {
	session := s.Session()
	expanded, err := s.Expand(headerValue)
	if err != nil {
		return err
	}
	expanded = strings.ReplaceAll(expanded, `\"`, `"`)
	session.Header.Set(headerName, expanded)
	session.Header.Del("Authorization")
	session.Header.Set("Accept", "*/*")
	savedUser := session.TestUser
	session.TestUser = nil
	err = s.sendHTTPRequest(method, path)
	session.TestUser = savedUser
	return err
}

func (s *TestScenario) iSetTheHeaderTo(name, value string) error {
	expanded, err := s.Expand(value)
	if err != nil {
		return err
	}
	s.Session().Header.Set(name, expanded)
	return nil
}

func (s *TestScenario) iWaitUpToSecondsForAResponseJSONEvent(timeout float64) error {
	session := s.Session()
	if !session.EventStream {
		return fmt.Errorf("the last http request was not performed as a json event stream")
	}

	session.respJSON = nil
	session.RespBytes = session.RespBytes[0:0]

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout*float64(time.Second)))
	defer cancel()

	select {
	case event := <-session.EventStreamEvents:
		session.respJSON = event
		var err error
		session.RespBytes, err = json.Marshal(event)
		if err != nil {
			return err
		}
	case <-ctx.Done():
	}
	return nil
}

func (s *TestScenario) iWaitUpToSecondsForAGETOnPathResponseCodeToMatch(timeout float64, path string, expected int) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout*float64(time.Second)))
	defer cancel()

	for {
		err := s.sendHTTPRequest("GET", path)
		if err == nil {
			err = s.theResponseCodeShouldBe(expected)
			if err == nil {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return nil
		default:
			time.Sleep(time.Duration(timeout * float64(time.Second) / 10.0))
		}
	}
}

func (s *TestScenario) iWaitUpToSecondsForAGETOnPathResponseSelectionToMatch(timeout float64, path, selection, expected string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout*float64(time.Second)))
	defer cancel()

	for {
		err := s.sendHTTPRequest("GET", path)
		if err == nil {
			err = s.theSelectionFromTheResponseShouldMatch(selection, expected)
			if err == nil {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return nil
		default:
			time.Sleep(time.Duration(timeout * float64(time.Second) / 10.0))
		}
	}
}

func (s *TestScenario) iWaitUpToSecondsForAGETOnPathToRespondWithJSON(timeout float64, path string, expectedJSON *godog.DocString) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout*float64(time.Second)))
	defer cancel()

	time.Sleep(100 * time.Millisecond)

	var lastErr error
	for {
		err := s.sendHTTPRequest("GET", path)
		if err == nil {
			err = s.theResponseCodeShouldBe(200)
			if err == nil {
				err = s.theResponseShouldMatchJSON(expectedJSON.Content)
				if err == nil {
					return nil
				}
			}
		}
		lastErr = err

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("condition not met after %.f seconds: %w", timeout, lastErr)
			}
			return fmt.Errorf("condition not met after %.f seconds (no response received)", timeout)
		default:
			time.Sleep(1 * time.Second)
		}
	}
}
