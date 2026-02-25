package bdd

import (
	"fmt"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		m := &membershipSteps{s: s}
		ctx.Step(`^I share the conversation with user "([^"]*)" with request:$`, m.iShareTheConversationWithUserWithRequest)
		ctx.Step(`^I share conversation "([^"]*)" with user "([^"]*)" with request:$`, m.iShareConversationWithUserWithRequest)
		ctx.Step(`^I share that conversation with user "([^"]*)" with request:$`, m.iShareTheConversationWithUserWithRequest)
		ctx.Step(`^I share the conversation with user "([^"]*)" and access level "([^"]*)"$`, m.iShareTheConversationWithUserAndAccessLevel)
		ctx.Step(`^the conversation is shared with user "([^"]*)" with access level "([^"]*)"$`, m.theConversationIsSharedWithUserWithAccessLevel)
		ctx.Step(`^I list memberships for the conversation$`, m.iListMembershipsForTheConversation)
		ctx.Step(`^I list memberships for conversation "([^"]*)"$`, m.iListMembershipsForConversation)
		ctx.Step(`^I list memberships for that conversation$`, m.iListMembershipsForTheConversation)
		ctx.Step(`^I update membership for user "([^"]*)" with request:$`, m.iUpdateMembershipForUserWithRequest)
		ctx.Step(`^I delete membership for user "([^"]*)"$`, m.iDeleteMembershipForUser)
	})
}

type membershipSteps struct {
	s *cucumber.TestScenario
}

func (m *membershipSteps) iShareTheConversationWithUserWithRequest(_ string, body *godog.DocString) error {
	convID, err := m.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return m.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/memberships", body, false, true)
}

func (m *membershipSteps) iShareConversationWithUserWithRequest(convID, _ string, body *godog.DocString) error {
	expanded, err := m.s.Expand(convID)
	if err != nil {
		return err
	}
	return m.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+expanded+"/memberships", body, false, true)
}

func (m *membershipSteps) iShareTheConversationWithUserAndAccessLevel(userID, accessLevel string) error {
	body := fmt.Sprintf(`{"userId": %q, "accessLevel": %q}`, userID, accessLevel)
	convID, err := m.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return m.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/memberships", &godog.DocString{Content: body}, false, false)
}

func (m *membershipSteps) theConversationIsSharedWithUserWithAccessLevel(userID, accessLevel string) error {
	// Use the conversation owner to share (they have manager access)
	savedUser := m.s.CurrentUser
	if owner, ok := m.s.Variables["conversationOwner"].(string); ok {
		a := &authSteps{s: m.s}
		_ = a.iAmAuthenticatedAsUser(owner)
	}

	err := m.iShareTheConversationWithUserAndAccessLevel(userID, accessLevel)

	// Restore user
	m.s.CurrentUser = savedUser
	return err
}

func (m *membershipSteps) iListMembershipsForTheConversation() error {
	convID, err := m.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return m.s.SendHTTPRequestWithJSONBodyAndStyle("GET", "/v1/conversations/"+convID+"/memberships", nil, false, true)
}

func (m *membershipSteps) iListMembershipsForConversation(convID string) error {
	expanded, err := m.s.Expand(convID)
	if err != nil {
		return err
	}
	return m.s.SendHTTPRequestWithJSONBodyAndStyle("GET", "/v1/conversations/"+expanded+"/memberships", nil, false, true)
}

func (m *membershipSteps) iUpdateMembershipForUserWithRequest(userID string, body *godog.DocString) error {
	convID, err := m.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return m.s.SendHTTPRequestWithJSONBodyAndStyle("PATCH", fmt.Sprintf("/v1/conversations/%s/memberships/%s", convID, userID), body, false, true)
}

func (m *membershipSteps) iDeleteMembershipForUser(userID string) error {
	convID, err := m.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return m.s.SendHTTPRequestWithJSONBodyAndStyle("DELETE", fmt.Sprintf("/v1/conversations/%s/memberships/%s", convID, userID), nil, false, true)
}
