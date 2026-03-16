//go:build site_tests

package sitebdd

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/cucumber/godog"
	"github.com/google/uuid"
)

// registerCheckpointSteps registers checkpoint lifecycle godog steps.
func registerCheckpointSteps(ctx *godog.ScenarioContext, s *SiteScenario) {
	ctx.Step(`^the docs scenario uses the unix socket memory service$`, s.docsScenarioUsesUnixSocketMemoryService)
	ctx.Step(`^checkpoint "([^"]*)" is active$`, s.checkpointIsActive)
	ctx.Step(`^I build the checkpoint$`, s.iBuildTheCheckpoint)
	ctx.Step(`^I build the checkpoint with "([^"]*)"$`, s.iBuildTheCheckpointWith)
	ctx.Step(`^the build should succeed$`, s.theBuildShouldSucceed)
	ctx.Step(`^I start the checkpoint$`, s.iStartTheCheckpoint)
	ctx.Step(`^I start the checkpoint on port (\d+)$`, s.iStartTheCheckpointOnPort)
	ctx.Step(`^the application should be running$`, s.theApplicationShouldBeRunning)
	ctx.Step(`^I stop the checkpoint$`, s.iStopTheCheckpoint)

	ctx.Before(func(ctx context.Context, sc *godog.Scenario) (context.Context, error) {
		s.ScenarioName = sc.Name
		s.WaveID = waveIDFromScenario(sc)
		return ctx, nil
	})

	// Cleanup after each scenario (handles panics / step failures)
	ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		s.stopCheckpoint(err != nil)
		s.finishWave()
		return ctx, nil
	})
}

func (s *SiteScenario) docsScenarioUsesUnixSocketMemoryService() error {
	s.UseUnixSocketMemoryService = true
	return nil
}

// checkpointIsActive sets the checkpoint, allocates a unique port and user suffix.
func (s *SiteScenario) checkpointIsActive(checkpointID string) error {
	s.CheckpointID = checkpointID
	s.CheckpointPath = filepath.Join(s.ProjectRoot, checkpointID)
	s.CheckpointPort = allocatePort()
	s.checkpointPathClaimed = false

	// Generate a short unique ID for user isolation
	uid := uuid.New().String()
	uid = strings.ReplaceAll(uid, "-", "")
	if len(uid) > 8 {
		uid = uid[:8]
	}
	s.ScenarioUID = uid

	// Decide record vs. playback before starting
	s.Recording = s.shouldRecord()
	s.Wave = globalScenarioWaveCoordinator.Enter(s.WaveID)

	if !fileExists(s.CheckpointPath) {
		return fmt.Errorf("checkpoint directory does not exist: %s", s.CheckpointPath)
	}
	if err := s.claimCheckpointPath(); err != nil {
		return fmt.Errorf("checkpoint isolation conflict: %w", err)
	}

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
	s.markWaveReady()
	return nil
}

func (s *SiteScenario) iStopTheCheckpoint() error {
	s.stopCheckpoint(false)
	return nil
}

func (s *SiteScenario) markWaveReady() {
	if s.Wave == nil || s.waveReady {
		return
	}
	globalScenarioWaveCoordinator.MarkReady(s.Wave)
	s.waveReady = true
}

func (s *SiteScenario) finishWave() {
	if s.Wave == nil {
		return
	}
	s.markWaveReady()
	globalScenarioWaveCoordinator.Finish(s.Wave)
	s.Wave = nil
}

func waveIDFromScenario(sc *godog.Scenario) int {
	for _, tag := range sc.Tags {
		name := strings.TrimPrefix(tag.Name, "@")
		if !strings.HasPrefix(name, "wave_") {
			continue
		}
		waveID, err := strconv.Atoi(strings.TrimPrefix(name, "wave_"))
		if err == nil && waveID > 0 {
			return waveID
		}
	}
	return 0
}
