package bdd

// Static role users for non-OIDC runners. The wildcard suffixes (alice-*, dave-*, ...)
// keep roles on the scenario-isolated subjects derived from each base user. alice is admin, auditor and
// indexer; charlie is auditor; dave is indexer. Use bob or erin when a scenario must
// prove ordinary user-to-user isolation.

func bddAdminUsers() string {
	return "alice,alice-*"
}

func bddAuditorUsers() string {
	return "alice,alice-*,charlie,charlie-*"
}

func bddIndexerUsers() string {
	return "alice,alice-*,dave,dave-*"
}
