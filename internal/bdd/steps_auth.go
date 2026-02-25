package bdd

import (
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		a := &authSteps{s: s}
		ctx.Step(`^I am authenticated as user "([^"]*)"$`, a.iAmAuthenticatedAsUser)
		ctx.Step(`^I am authenticated as admin user "([^"]*)"$`, a.iAmAuthenticatedAsAdminUser)
		ctx.Step(`^I am authenticated as agent with API key "([^"]*)"$`, a.iAmAuthenticatedAsAgentWithAPIKey)
		ctx.Step(`^I am authenticated as auditor user "([^"]*)"$`, a.iAmAuthenticatedAsAuditorUser)
		ctx.Step(`^I am authenticated as indexer user "([^"]*)"$`, a.iAmAuthenticatedAsIndexerUser)
		ctx.Step(`^I authenticate as user "([^"]*)"$`, a.iAmAuthenticatedAsUser)
	})
}

type authSteps struct {
	s *cucumber.TestScenario
}

func (a *authSteps) setUser(userID string) {
	a.s.Suite.Mu.Lock()
	defer a.s.Suite.Mu.Unlock()
	if a.s.Users[userID] == nil {
		a.s.Users[userID] = &cucumber.TestUser{
			Name:    userID,
			Subject: userID, // Bearer token = user ID for API key auth
		}
	}
	a.s.CurrentUser = userID
}

func (a *authSteps) iAmAuthenticatedAsUser(userID string) error {
	a.setUser(userID)
	session := a.s.Session()
	session.Header.Del("X-Client-ID")
	return nil
}

func (a *authSteps) iAmAuthenticatedAsAdminUser(userID string) error {
	a.setUser(userID)
	session := a.s.Session()
	session.Header.Del("X-Client-ID")
	return nil
}

func (a *authSteps) iAmAuthenticatedAsAgentWithAPIKey(apiKey string) error {
	// In Java, agent API keys map to the conversation owner. In Go, the bearer
	// token IS the user ID. Keep the current user (conversation owner) as the
	// bearer identity and only set X-Client-ID to distinguish agent mode.
	session := a.s.Session()
	session.Header.Set("X-Client-ID", apiKey)
	return nil
}

func (a *authSteps) iAmAuthenticatedAsAuditorUser(userID string) error {
	a.setUser(userID)
	session := a.s.Session()
	session.Header.Del("X-Client-ID")
	return nil
}

func (a *authSteps) iAmAuthenticatedAsIndexerUser(userID string) error {
	a.setUser(userID)
	session := a.s.Session()
	session.Header.Del("X-Client-ID")
	return nil
}
