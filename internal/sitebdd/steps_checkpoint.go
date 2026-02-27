//go:build site_tests

package sitebdd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cucumber/godog"
	"github.com/google/uuid"
)

// registerCheckpointSteps registers checkpoint lifecycle godog steps.
func registerCheckpointSteps(ctx *godog.ScenarioContext, s *SiteScenario) {
	ctx.Step(`^checkpoint "([^"]*)" is active$`, s.checkpointIsActive)
	ctx.Step(`^I build the checkpoint$`, s.iBuildTheCheckpoint)
	ctx.Step(`^I build the checkpoint with "([^"]*)"$`, s.iBuildTheCheckpointWith)
	ctx.Step(`^the build should succeed$`, s.theBuildShouldSucceed)
	ctx.Step(`^I start the checkpoint$`, s.iStartTheCheckpoint)
	ctx.Step(`^I start the checkpoint on port (\d+)$`, s.iStartTheCheckpointOnPort)
	ctx.Step(`^the application should be running$`, s.theApplicationShouldBeRunning)
	ctx.Step(`^I stop the checkpoint$`, s.iStopTheCheckpoint)

	// Cleanup after each scenario (handles panics / step failures)
	ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		s.stopCheckpoint()
		return ctx, nil
	})
}

// checkpointIsActive sets the checkpoint, allocates a unique port and user suffix.
func (s *SiteScenario) checkpointIsActive(checkpointID string) error {
	s.CheckpointID = checkpointID
	s.CheckpointPath = filepath.Join(s.ProjectRoot, checkpointID)
	s.CheckpointPort = allocatePort()

	// Generate a short unique ID for user isolation
	uid := uuid.New().String()
	uid = strings.ReplaceAll(uid, "-", "")
	if len(uid) > 8 {
		uid = uid[:8]
	}
	s.ScenarioUID = uid

	// Decide record vs. playback before starting
	s.Recording = s.shouldRecord()

	if !fileExists(s.CheckpointPath) {
		return fmt.Errorf("checkpoint directory does not exist: %s", s.CheckpointPath)
	}

	mode := "playback"
	if s.Recording {
		mode = "RECORDING"
	}
	fmt.Printf("[sitebdd] Scenario UID=%s  checkpoint=%s  port=%d  mode=%s\n",
		s.ScenarioUID, checkpointID, s.CheckpointPort, mode)
	return nil
}

func (s *SiteScenario) iBuildTheCheckpoint() error {
	return s.buildCheckpoint()
}

func (s *SiteScenario) iBuildTheCheckpointWith(buildCmd string) error {
	args := strings.Fields(buildCmd)
	return s.buildCheckpoint(args...)
}

func (s *SiteScenario) theBuildShouldSucceed() error {
	if s.buildExitCode != 0 {
		return fmt.Errorf("build failed with exit code %d", s.buildExitCode)
	}
	return nil
}

func (s *SiteScenario) iStartTheCheckpoint() error {
	return s.startCheckpoint()
}

func (s *SiteScenario) iStartTheCheckpointOnPort(port int) error {
	// Allow the feature file to specify an explicit port (legacy support).
	// In practice, the generated feature files use the no-arg form.
	s.CheckpointPort = port
	return s.startCheckpoint()
}

func (s *SiteScenario) theApplicationShouldBeRunning() error {
	if s.checkpointCmd == nil || s.checkpointCmd.Process == nil {
		return fmt.Errorf("checkpoint process is not running")
	}
	// Check process is still alive by sending signal 0
	if err := s.checkpointCmd.Process.Signal(syscall.Signal(0)); err != nil {
		return fmt.Errorf("checkpoint process has exited: %v", err)
	}
	return nil
}

func (s *SiteScenario) iStopTheCheckpoint() error {
	s.stopCheckpoint()
	return nil
}
