package bdd

import (
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
)

func clearFeatureDB(t *testing.T, db cucumber.TestDB) {
	t.Helper()
	if db == nil {
		return
	}
	if err := db.ClearAll(context.Background()); err != nil {
		t.Fatalf("clear feature datastore: %v", err)
	}
}
