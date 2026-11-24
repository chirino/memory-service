package bdd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
	"github.com/google/uuid"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		a := &attachmentSteps{s: s}
		ctx.Step(`^I upload a file "([^"]*)" with content type "([^"]*)" and content "([^"]*)"$`, a.iUploadAFile)
		ctx.Step(`^I upload a file "([^"]*)" with content type "([^"]*)" and content "([^"]*)" with id "([^"]*)"$`, a.iUploadAFileWithID)
		ctx.Step(`^I start a stalled upload of file "([^"]*)" with content type "([^"]*)" and content "([^"]*)" with id "([^"]*)"$`, a.iStartStalledUpload)
		ctx.Step(`^I finish the stalled upload$`, a.iFinishStalledUpload)
		ctx.Step(`^I abandon the stalled upload$`, a.iAbandonStalledUpload)
		ctx.Step(`^I wait for attachment "([^"]*)" to have status "([^"]*)"$`, a.iWaitForAttachmentStatus)
		ctx.Step(`^set "([^"]*)" to a new UUID$`, a.setToNewUUID)

		ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
			a.abandonStalledUpload()
			return ctx, nil
		})
	})
}

type attachmentSteps struct {
	s       *cucumber.TestScenario
	stalled *stalledUpload
}

// stalledUpload is a multipart upload whose body has been partly sent. The
// server buffers the whole body before its handler runs, so no attachment row
// exists until finish is called.
type stalledUpload struct {
	writer *io.PipeWriter
	form   *multipart.Writer
	done   chan stalledUploadResult
	url    string
}

type stalledUploadResult struct {
	resp  *http.Response
	bytes []byte
	err   error
}

func (a *attachmentSteps) setToNewUUID(name string) error {
	a.s.Variables[name] = uuid.NewString()
	return nil
}

func (a *attachmentSteps) iUploadAFile(filename, contentType, content string) error {
	return a.upload(filename, contentType, content, nil)
}

func (a *attachmentSteps) iUploadAFileWithID(filename, contentType, content, id string) error {
	expanded, err := a.s.Expand(id)
	if err != nil {
		return err
	}
	return a.upload(filename, contentType, content, &expanded)
}

// writeUploadParts writes the optional id form field, then the file part with its own
// content type. A non-nil id is always sent, even when it is empty, so a blank
// field stays distinguishable from an absent one.
func writeUploadParts(writer *multipart.Writer, filename, contentType string, id *string) (io.Writer, error) {
	if id != nil {
		if err := writer.WriteField("id", *id); err != nil {
			return nil, err
		}
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	h.Set("Content-Type", contentType)
	return writer.CreatePart(h)
}

func (a *attachmentSteps) upload(filename, contentType, content string, id *string) error {
	// Build multipart form body with proper content type on the file part.
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writeUploadParts(writer, filename, contentType, id)
	if err != nil {
		return err
	}
	if _, err = part.Write([]byte(content)); err != nil {
		return err
	}
	_ = writer.Close()

	session := a.s.Session()
	a.resetResponse()

	fullURL := a.s.APIBaseURL() + "/v1/attachments"
	req, err := http.NewRequestWithContext(context.Background(), "POST", fullURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if session.TestUser != nil && session.TestUser.Subject != "" {
		req.Header.Set("Authorization", "Bearer "+session.TestUser.Subject)
	}

	resp, err := session.Client.Do(req)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	return a.recordResponse(fullURL, resp, body)
}

func (a *attachmentSteps) resetResponse() {
	session := a.s.Session()
	if session.Resp != nil {
		_ = session.Resp.Body.Close()
	}
	session.Resp = nil
	session.RespBytes = nil
	session.SetRespBytes(nil)
}

func (a *attachmentSteps) recordResponse(fullURL string, resp *http.Response, body []byte) error {
	session := a.s.Session()
	session.Resp = resp
	session.RespBytes = body
	normalized, err := a.s.NormalizeResponseBody(fullURL, resp.Header.Get("Content-Type"), body)
	if err != nil {
		return err
	}
	session.SetRespBytes(normalized)
	return nil
}

// iStartStalledUpload sends the id field, the file part header and the content
// of a multipart upload, then withholds the closing boundary so the server
// cannot finish reading the request.
func (a *attachmentSteps) iStartStalledUpload(filename, contentType, content, id string) error {
	if a.stalled != nil {
		return fmt.Errorf("a stalled upload is already open")
	}
	expanded, err := a.s.Expand(id)
	if err != nil {
		return err
	}
	session := a.s.Session()

	// Same identity as the session, including any agent client header.
	header := make(http.Header)
	for k, v := range session.Header {
		header[k] = append([]string(nil), v...)
	}
	if header.Get("Authorization") == "" && session.TestUser != nil && session.TestUser.Subject != "" {
		header.Set("Authorization", "Bearer "+session.TestUser.Subject)
	}

	pr, pw := io.Pipe()
	form := multipart.NewWriter(pw)
	fullURL := a.s.APIBaseURL() + "/v1/attachments"
	req, err := http.NewRequestWithContext(context.Background(), "POST", fullURL, pr)
	if err != nil {
		return err
	}
	req.Header = header
	req.Header.Set("Content-Type", form.FormDataContentType())

	up := &stalledUpload{writer: pw, form: form, done: make(chan stalledUploadResult, 1), url: fullURL}
	go func() {
		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			up.done <- stalledUploadResult{err: err}
			return
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		up.done <- stalledUploadResult{resp: resp, bytes: body, err: err}
	}()

	part, err := writeUploadParts(form, filename, contentType, &expanded)
	if err != nil {
		_ = pw.CloseWithError(err)
		return err
	}
	if _, err := part.Write([]byte(content)); err != nil {
		_ = pw.CloseWithError(err)
		return err
	}
	a.stalled = up
	// Give the server time to receive the headers and the part so the request is
	// in flight, not merely unsent.
	time.Sleep(300 * time.Millisecond)
	return nil
}

// iFinishStalledUpload sends the closing boundary and records the response as
// the current response.
func (a *attachmentSteps) iFinishStalledUpload() error {
	up := a.stalled
	if up == nil {
		return fmt.Errorf("no stalled upload is open")
	}
	a.stalled = nil
	if err := up.form.Close(); err != nil {
		_ = up.writer.CloseWithError(err)
		return err
	}
	if err := up.writer.Close(); err != nil {
		return err
	}
	select {
	case res := <-up.done:
		if res.err != nil {
			return res.err
		}
		a.resetResponse()
		return a.recordResponse(up.url, res.resp, res.bytes)
	case <-time.After(30 * time.Second):
		return fmt.Errorf("stalled upload did not complete within 30 seconds")
	}
}

func (a *attachmentSteps) iAbandonStalledUpload() error {
	if a.stalled == nil {
		return fmt.Errorf("no stalled upload is open")
	}
	a.abandonStalledUpload()
	return nil
}

func (a *attachmentSteps) abandonStalledUpload() {
	up := a.stalled
	if up == nil {
		return
	}
	a.stalled = nil
	_ = up.writer.CloseWithError(io.ErrUnexpectedEOF)
	select {
	case <-up.done:
	case <-time.After(5 * time.Second):
	}
}

// iWaitForAttachmentStatus polls the REST metadata lookup until the attachment
// reports the wanted status. The last lookup stays the current response.
func (a *attachmentSteps) iWaitForAttachmentStatus(rawID, want string) error {
	id, err := a.s.Expand(rawID)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	lastStatus := ""
	for {
		if err := a.s.SendHTTPRequestWithJSONBody("GET", "/v1/attachments/"+id+"/metadata", nil); err != nil {
			return err
		}
		session := a.s.Session()
		if session.Resp != nil && session.Resp.StatusCode == http.StatusOK {
			respJSON, err := session.RespJSON()
			if err != nil {
				return err
			}
			if m, ok := respJSON.(map[string]any); ok {
				lastStatus, _ = m["status"].(string)
				if lastStatus == want {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("attachment %s did not reach status %q within 10 seconds (last status %q, last response %s)", id, want, lastStatus, string(session.RespBytes))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
