package bdd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		a := &attachmentSteps{s: s}
		ctx.Step(`^I upload a file "([^"]*)" with content type "([^"]*)" and content "([^"]*)"$`, a.iUploadAFile)
	})
}

type attachmentSteps struct {
	s *cucumber.TestScenario
}

func (a *attachmentSteps) iUploadAFile(filename, contentType, content string) error {
	// Build multipart form body with proper content type on the file part.
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	h.Set("Content-Type", contentType)
	part, err := writer.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = part.Write([]byte(content))
	if err != nil {
		return err
	}
	_ = writer.Close()

	session := a.s.Session()

	// Reset response state
	if session.Resp != nil {
		_ = session.Resp.Body.Close()
	}
	session.Resp = nil
	session.RespBytes = nil
	session.SetRespBytes(nil)

	fullURL := a.s.Suite.APIURL + "/v1/attachments"
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
	session.Resp = resp
	defer func() { _ = resp.Body.Close() }()
	session.RespBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	session.SetRespBytes(session.RespBytes)

	return nil
}
