Feature: Organizations and Teams REST API
  As a user
  I want to create organizations, manage members, and create teams
  So that I can collaborate with others in a multi-tenant environment

  # ---- Organization CRUD ----

  Scenario: Create an organization
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {
      "name": "Acme Corp",
      "slug": "acme-corp",
      "metadata": {"industry": "tech"}
    }
    """
    Then the response status should be 201
    And the response body field "name" should be "Acme Corp"
    And the response body field "slug" should be "acme-corp"
    And the response body field "role" should be "owner"
    And the response body field "id" should not be null

  Scenario: List user's organizations
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Org A", "slug": "org-a"}
    """
    Then the response status should be 201
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Org B", "slug": "org-b"}
    """
    Then the response status should be 201
    When I call GET "/v1/organizations"
    Then the response status should be 200

  Scenario: Get organization detail
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Detail Org", "slug": "detail-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call GET "/v1/organizations/${orgId}"
    Then the response status should be 200
    And the response body field "name" should be "Detail Org"
    And the response body field "role" should be "owner"

  Scenario: Update an organization
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Old Name", "slug": "old-slug"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call PATCH "/v1/organizations/${orgId}" with body:
    """
    {"name": "New Name"}
    """
    Then the response status should be 200
    And the response body field "name" should be "New Name"

  Scenario: Delete an organization (owner only)
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "To Delete", "slug": "to-delete"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call DELETE "/v1/organizations/${orgId}"
    Then the response status should be 204

  Scenario: Non-member cannot access organization
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Private Org", "slug": "private-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    Given I am authenticated as user "bob"
    When I call GET "/v1/organizations/${orgId}"
    Then the response status should be 403

  # ---- Organization Members ----

  Scenario: Add a member to an organization
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Team Org", "slug": "team-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call POST "/v1/organizations/${orgId}/members" with body:
    """
    {"userId": "bob", "role": "member"}
    """
    Then the response status should be 201
    And the response body field "userId" should be "bob"
    And the response body field "role" should be "member"

  Scenario: List organization members
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Members Org", "slug": "members-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call POST "/v1/organizations/${orgId}/members" with body:
    """
    {"userId": "charlie", "role": "admin"}
    """
    Then the response status should be 201
    When I call GET "/v1/organizations/${orgId}/members"
    Then the response status should be 200

  Scenario: Update member role
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Role Org", "slug": "role-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call POST "/v1/organizations/${orgId}/members" with body:
    """
    {"userId": "bob", "role": "member"}
    """
    Then the response status should be 201
    When I call PATCH "/v1/organizations/${orgId}/members/bob" with body:
    """
    {"role": "admin"}
    """
    Then the response status should be 200
    And the response body field "role" should be "admin"

  Scenario: Remove member from organization
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Remove Org", "slug": "remove-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call POST "/v1/organizations/${orgId}/members" with body:
    """
    {"userId": "dave", "role": "member"}
    """
    Then the response status should be 201
    When I call DELETE "/v1/organizations/${orgId}/members/dave"
    Then the response status should be 204

  # ---- Teams ----

  Scenario: Create a team within an organization
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Teams Org", "slug": "teams-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call POST "/v1/organizations/${orgId}/teams" with body:
    """
    {"name": "Engineering", "slug": "engineering"}
    """
    Then the response status should be 201
    And the response body field "name" should be "Engineering"
    And the response body field "slug" should be "engineering"

  Scenario: List teams in an organization
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "List Teams Org", "slug": "list-teams-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call POST "/v1/organizations/${orgId}/teams" with body:
    """
    {"name": "Team A", "slug": "team-a"}
    """
    Then the response status should be 201
    When I call GET "/v1/organizations/${orgId}/teams"
    Then the response status should be 200

  Scenario: Add and list team members
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "TM Org", "slug": "tm-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    # Add bob as org member first
    When I call POST "/v1/organizations/${orgId}/members" with body:
    """
    {"userId": "bob", "role": "member"}
    """
    Then the response status should be 201
    # Create team
    When I call POST "/v1/organizations/${orgId}/teams" with body:
    """
    {"name": "Dev Team", "slug": "dev-team"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "teamId"
    # Add bob to team
    When I call POST "/v1/organizations/${orgId}/teams/${teamId}/members" with body:
    """
    {"userId": "bob"}
    """
    Then the response status should be 201
    When I call GET "/v1/organizations/${orgId}/teams/${teamId}/members"
    Then the response status should be 200

  Scenario: Remove team member
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "RM TM Org", "slug": "rm-tm-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call POST "/v1/organizations/${orgId}/members" with body:
    """
    {"userId": "charlie", "role": "member"}
    """
    Then the response status should be 201
    When I call POST "/v1/organizations/${orgId}/teams" with body:
    """
    {"name": "Ops Team", "slug": "ops-team"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "teamId"
    When I call POST "/v1/organizations/${orgId}/teams/${teamId}/members" with body:
    """
    {"userId": "charlie"}
    """
    Then the response status should be 201
    When I call DELETE "/v1/organizations/${orgId}/teams/${teamId}/members/charlie"
    Then the response status should be 204

  # ---- Org-scoped conversations ----

  Scenario: Create an org-scoped conversation
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Conv Org", "slug": "conv-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call POST "/v1/conversations?organizationId=${orgId}" with body:
    """
    {"title": "Org Conversation"}
    """
    Then the response status should be 201

  # ---- Access control ----

  Scenario: Regular member cannot delete organization
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "No Delete", "slug": "no-delete"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call POST "/v1/organizations/${orgId}/members" with body:
    """
    {"userId": "bob", "role": "member"}
    """
    Then the response status should be 201
    Given I am authenticated as user "bob"
    When I call DELETE "/v1/organizations/${orgId}"
    Then the response status should be 403

  Scenario: Non-org member cannot be added to team
    Given I am authenticated as user "alice"
    When I call POST "/v1/organizations" with body:
    """
    {"name": "Restrict Org", "slug": "restrict-org"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "orgId"
    When I call POST "/v1/organizations/${orgId}/teams" with body:
    """
    {"name": "Restricted Team", "slug": "restricted-team"}
    """
    Then the response status should be 201
    Given I save the response body field "id" as "teamId"
    # Try to add eve who is not an org member
    When I call POST "/v1/organizations/${orgId}/teams/${teamId}/members" with body:
    """
    {"userId": "eve"}
    """
    Then the response status should be 403
