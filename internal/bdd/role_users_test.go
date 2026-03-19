package bdd

func bddAdminUsers() string {
	return "alice,alice-*"
}

func bddAuditorUsers() string {
	return "alice,alice-*,charlie,charlie-*"
}

func bddIndexerUsers() string {
	return "alice,alice-*,dave,dave-*"
}
