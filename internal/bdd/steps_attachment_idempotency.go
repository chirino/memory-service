package bdd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		a := &attachmentIdempotencySteps{s: s, sources: map[string]*attachmentSource{}}

		ctx.Step(`^I upload a file "([^"]*)" with content type "([^"]*)" and content "([^"]*)" with id "([^"]*)" concurrently (\d+) times$`, a.iUploadConcurrently)
		ctx.Step(`^exactly one concurrent upload should have status (\d+) and the rest should have status (\d+)$`, a.exactlyOneConcurrentUploadShouldHaveStatus)
		ctx.Step(`^every concurrent upload with status (\d+) should have response body field "([^"]*)" equal to "([^"]*)"$`, a.everyConcurrentUploadWithStatusShouldHaveField)

		ctx.Step(`^a source server serving "([^"]*)" with content type "([^"]*)" as "([^"]*)"$`, a.aSourceServer)
		ctx.Step(`^a gated source server serving "([^"]*)" with content type "([^"]*)" as "([^"]*)"$`, a.aGatedSourceServer)
		ctx.Step(`^a source server failing with status (\d+) as "([^"]*)"$`, a.aFailingSourceServer)
		ctx.Step(`^I release the source server "([^"]*)"$`, a.iReleaseTheSourceServer)
		ctx.Step(`^I wait for the source server "([^"]*)" to receive (\d+) requests?$`, a.iWaitForSourceRequests)
		ctx.Step(`^the source server "([^"]*)" should have received exactly (\d+) requests?$`, a.theSourceServerShouldHaveReceivedExactly)

		ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
			a.closeSources()
			return ctx, nil
		})
	})
}

type attachmentIdempotencySteps struct {
	s          *cucumber.TestScenario
	uploads    []concurrentUploadResult
	sources    map[string]*attachmentSource
	sourcesMux sync.Mutex
}

type concurrentUploadResult struct {
	status int
	body   string
}

// attachmentSource is an HTTP server the attachment service downloads from. It
// counts every request it receives. A gated server holds each response until it
// is released, which keeps a URL attachment in `downloading`.
type attachmentSource struct {
	srv      *httptest.Server
	hits     atomic.Int64
	gate     chan struct{}
	gateOnce sync.Once
}

func (src *attachmentSource) release() {
	if src.gate != nil {
		src.gateOnce.Do(func() { close(src.gate) })
	}
}

// iUploadConcurrently sends count identical multipart uploads with one ID, all
// released together, under the scenario's identity (every session header).
func (a *attachmentIdempotencySteps) iUploadConcurrently(filename, contentType, content, id string, count int) error {
	expandedID, err := a.s.Expand(id)
	if err != nil {
		return err
	}
	session := a.s.Session()
	baseHeaders := make(http.Header)
	for k, v := range session.Header {
		baseHeaders[k] = append([]string(nil), v...)
	}
	if baseHeaders.Get("Authorization") == "" && session.TestUser != nil && session.TestUser.Subject != "" {
		baseHeaders.Set("Authorization", "Bearer "+session.TestUser.Subject)
	}
	// Each request needs its own multipart boundary, so Content-Type is set per request.
	baseHeaders.Del("Content-Type")

	a.uploads = make([]concurrentUploadResult, count)
	apiURL := a.s.APIBaseURL() + "/v1/attachments"

	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(count)
	for i := 0; i < count; i++ {
		done.Add(1)
		go func(idx int) {
			defer done.Done()
			var buf bytes.Buffer
			form := multipart.NewWriter(&buf)
			part, err := writeUploadParts(form, filename, contentType, &expandedID)
			if err == nil {
				_, err = part.Write([]byte(content))
			}
			if err == nil {
				err = form.Close()
			}
			ready.Done()
			if err != nil {
				a.uploads[idx] = concurrentUploadResult{body: err.Error()}
				return
			}
			<-start
			req, err := http.NewRequest(http.MethodPost, apiURL, &buf)
			if err != nil {
				a.uploads[idx] = concurrentUploadResult{body: err.Error()}
				return
			}
			req.Header = make(http.Header)
			for k, v := range baseHeaders {
				req.Header[k] = append([]string(nil), v...)
			}
			req.Header.Set("Content-Type", form.FormDataContentType())
			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				a.uploads[idx] = concurrentUploadResult{body: err.Error()}
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			a.uploads[idx] = concurrentUploadResult{status: resp.StatusCode, body: string(body)}
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
	return nil
}

// exactlyOneConcurrentUploadShouldHaveStatus is exact: one creation, and every
// other request is the conflict. Any other status, including a transport error
// (status 0), fails.
func (a *attachmentIdempotencySteps) exactlyOneConcurrentUploadShouldHaveStatus(first, rest int) error {
	if len(a.uploads) == 0 {
		return fmt.Errorf("no concurrent uploads were run")
	}
	firstCount := 0
	for i, res := range a.uploads {
		switch res.status {
		case first:
			firstCount++
		case rest:
		default:
			return fmt.Errorf("concurrent upload %d had status %d, expected %d or %d (body: %s)", i, res.status, first, rest, res.body)
		}
	}
	if firstCount != 1 {
		return fmt.Errorf("expected exactly one concurrent upload with status %d, got %d (statuses: %v)", first, firstCount, a.statuses())
	}
	return nil
}

func (a *attachmentIdempotencySteps) statuses() []int {
	out := make([]int, len(a.uploads))
	for i, res := range a.uploads {
		out[i] = res.status
	}
	return out
}

func (a *attachmentIdempotencySteps) everyConcurrentUploadWithStatusShouldHaveField(status int, field, rawExpected string) error {
	expected, err := a.s.Expand(rawExpected)
	if err != nil {
		return err
	}
	matched := 0
	for i, res := range a.uploads {
		if res.status != status {
			continue
		}
		matched++
		var decoded map[string]any
		if err := json.Unmarshal([]byte(res.body), &decoded); err != nil {
			return fmt.Errorf("concurrent upload %d body is not a JSON object: %w (body: %s)", i, err, res.body)
		}
		got, ok := decoded[field].(string)
		if !ok || got != expected {
			return fmt.Errorf("concurrent upload %d field %q = %v, expected %q (body: %s)", i, field, decoded[field], expected, res.body)
		}
	}
	if matched == 0 {
		return fmt.Errorf("no concurrent upload had status %d (statuses: %v)", status, a.statuses())
	}
	return nil
}

func (a *attachmentIdempotencySteps) addSource(name string, src *attachmentSource) {
	a.sourcesMux.Lock()
	defer a.sourcesMux.Unlock()
	a.sources[name] = src
	a.s.Variables[name] = src.srv.URL + "/source.bin"
}

func (a *attachmentIdempotencySteps) aSourceServer(content, contentType, name string) error {
	return a.startSource(content, contentType, name, false)
}

func (a *attachmentIdempotencySteps) aGatedSourceServer(content, contentType, name string) error {
	return a.startSource(content, contentType, name, true)
}

func (a *attachmentIdempotencySteps) startSource(content, contentType, name string, gated bool) error {
	src := &attachmentSource{}
	if gated {
		src.gate = make(chan struct{})
	}
	src.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		src.hits.Add(1)
		if src.gate != nil {
			select {
			case <-src.gate:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(content))
	}))
	a.addSource(name, src)
	return nil
}

func (a *attachmentIdempotencySteps) aFailingSourceServer(status int, name string) error {
	src := &attachmentSource{}
	src.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		src.hits.Add(1)
		w.WriteHeader(status)
	}))
	a.addSource(name, src)
	return nil
}

func (a *attachmentIdempotencySteps) source(name string) (*attachmentSource, error) {
	a.sourcesMux.Lock()
	defer a.sourcesMux.Unlock()
	src := a.sources[name]
	if src == nil {
		return nil, fmt.Errorf("no source server named %q", name)
	}
	return src, nil
}

func (a *attachmentIdempotencySteps) iReleaseTheSourceServer(name string) error {
	src, err := a.source(name)
	if err != nil {
		return err
	}
	src.release()
	return nil
}

func (a *attachmentIdempotencySteps) iWaitForSourceRequests(name string, want int) error {
	src, err := a.source(name)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for src.hits.Load() < int64(want) {
		if time.Now().After(deadline) {
			return fmt.Errorf("source server %q received %d requests, expected at least %d", name, src.hits.Load(), want)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil
}

// theSourceServerShouldHaveReceivedExactly settles briefly first, so a download
// that a duplicate request wrongly started has time to reach the server.
func (a *attachmentIdempotencySteps) theSourceServerShouldHaveReceivedExactly(name string, want int) error {
	src, err := a.source(name)
	if err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	if got := src.hits.Load(); got != int64(want) {
		return fmt.Errorf("source server %q received %d requests, expected exactly %d", name, got, want)
	}
	return nil
}

func (a *attachmentIdempotencySteps) closeSources() {
	a.sourcesMux.Lock()
	sources := a.sources
	a.sources = map[string]*attachmentSource{}
	a.sourcesMux.Unlock()
	for _, src := range sources {
		// Release first: Close waits for in-flight handlers.
		src.release()
		src.srv.Close()
	}
}
