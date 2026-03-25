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
		ctx.Step(`^I am not authenticated$`, a.iAmNotAuthenticated)
		ctx.Step(`^I authenticate as user "([^"]*)"$`, a.iAmAuthenticatedAsUser)
	})
}

type authSteps struct {
	s *cucumber.TestScenario
}

type authSnapshot struct {
	currentUser string
	subject     string
	hasUser     bool
	clientID    string
}

func snapshotAuthState(s *cucumber.TestScenario) authSnapshot {
	snapshot := authSnapshot{
		currentUser: s.CurrentUser,
		clientID:    s.Session().Header.Get("X-Client-ID"),
	}
	if user := s.Users[s.CurrentUser]; user != nil {
		snapshot.subject = user.Subject
		snapshot.hasUser = true
	}
	return snapshot
}

func restoreAuthState(s *cucumber.TestScenario, snapshot authSnapshot) {
	s.CurrentUser = snapshot.currentUser
	if snapshot.hasUser {
		if s.Users[snapshot.currentUser] == nil {
			s.Users[snapshot.currentUser] = &cucumber.TestUser{Name: snapshot.currentUser}
		}
		s.Users[snapshot.currentUser].Subject = snapshot.subject
	}
	session := s.Session()
	if snapshot.clientID != "" {
		session.Header.Set("X-Client-ID", snapshot.clientID)
	} else {
		session.Header.Del("X-Client-ID")
	}
}

func (a *authSteps) setUser(userID string, isolated bool) {
	a.s.RegisterCanonicalUsers(userID)
	subject := userID
	if isolated {
		subject = a.s.IsolatedUser(userID)
	}
	a.s.Suite.Mu.Lock()
	defer a.s.Suite.Mu.Unlock()
	if a.s.Users[userID] == nil {
		a.s.Users[userID] = &cucumber.TestUser{
			Name:    userID,
			Subject: subject,
		}
	} else {
		a.s.Users[userID].Subject = subject
	}
	a.s.CurrentUser = userID
}

func (a *authSteps) iAmAuthenticatedAsUser(userID string) error {
	a.setUser(userID, true)
	session := a.s.Session()
	session.Header.Del("X-Client-ID")
	return nil
}

func (a *authSteps) iAmAuthenticatedAsAdminUser(userID string) error {
	a.setUser(userID, true)
	session := a.s.Session()
	session.Header.Del("X-Client-ID")
	return nil
}

func (a *authSteps) iAmAuthenticatedAsAgentWithAPIKey(apiKey string) error {
	// In Java, agent API keys map to the conversation owner. In Go, the bearer
	// token IS the user ID. Keep the current user (conversation owner) as the
	// bearer identity and only set X-Client-ID to distinguish agent mode.
	if currentUser := a.s.CurrentUser; currentUser != "" {
		if user := a.s.Users[currentUser]; user != nil && user.Subject == currentUser {
			user.Subject = a.s.IsolatedUser(currentUser)
		}
	}
	session := a.s.Session()
	session.Header.Set("X-Client-ID", a.s.IsolatedClientID(apiKey))
	return nil
}

func (a *authSteps) iAmAuthenticatedAsAuditorUser(userID string) error {
	a.setUser(userID, true)
	session := a.s.Session()
	session.Header.Del("X-Client-ID")
	return nil
}

func (a *authSteps) iAmAuthenticatedAsIndexerUser(userID string) error {
	a.setUser(userID, true)
	session := a.s.Session()
	session.Header.Del("X-Client-ID")
	return nil
}

func (a *authSteps) iAmNotAuthenticated() error {
	session := a.s.Session()
	session.Header.Del("Authorization")
	session.Header.Del("X-Client-ID")
	session.TestUser = nil
	a.s.CurrentUser = ""
	return nil
}
