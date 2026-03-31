package bdd

import (
	"context"
	"fmt"
	"time"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		cl := &clusteringSteps{s: s}
		ctx.Step(`^I wait for embeddings to be generated$`, cl.iWaitForEmbeddingsToBeGenerated)
	})
}

type clusteringSteps struct {
	s *cucumber.TestScenario
}

// iWaitForEmbeddingsToBeGenerated polls entry_embeddings until at least one row exists,
// or times out after 60 seconds. The BackgroundIndexer runs every 30s.
func (cl *clusteringSteps) iWaitForEmbeddingsToBeGenerated() error {
	if cl.s.Suite.DB == nil {
		return fmt.Errorf("no TestDB configured")
	}

	timeout := 60 * time.Second
	poll := 2 * time.Second
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		rows, err := cl.s.Suite.DB.ExecSQL(context.Background(),
			"SELECT COUNT(*) AS cnt FROM entry_embeddings")
		if err != nil {
			return fmt.Errorf("polling entry_embeddings: %w", err)
		}
		if len(rows) > 0 {
			if cnt, ok := rows[0]["cnt"]; ok {
				switch v := cnt.(type) {
				case int64:
					if v > 0 {
						return nil
					}
				case float64:
					if v > 0 {
						return nil
					}
				}
			}
		}
		time.Sleep(poll)
	}

	return fmt.Errorf("timed out waiting for embeddings after %v", timeout)
}
