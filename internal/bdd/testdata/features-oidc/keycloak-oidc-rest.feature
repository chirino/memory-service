@oidc
Feature: Keycloak OIDC integration for Go service
  As an API caller
  I want authentication and authorization to come from Keycloak-issued OIDC tokens
  So login and role-based access are enforced correctly

  Scenario: OIDC login fails with invalid credentials
    Given I attempt OIDC login as user "bob" with password "not-bob"
    Then OIDC login should fail

  Scenario: Invalid bearer JWT is rejected
    Given I set the "Authorization" header to "Bearer not.a.valid.jwt"
    When I call GET "/v1/conversations"
    Then the response status should be 401
    And the response body should contain "invalid JWT"

  Scenario: OIDC login allows basic user access
    Given I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/conversations"
    Then the response status should be 200

  Scenario: Non-admin OIDC user is forbidden from admin endpoints
    Given I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 403

  Scenario: Non-indexer OIDC user is forbidden from indexer endpoints
    Given I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/conversations/unindexed?limit=10"
    Then the response status should be 403

  Scenario: Admin role from Keycloak token grants admin and indexer access
    Given I login via OIDC as user "alice" with password "alice"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200
    When I call GET "/v1/conversations/unindexed?limit=10"
    Then the response status should be 200
