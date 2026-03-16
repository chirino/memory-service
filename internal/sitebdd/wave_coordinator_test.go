//go:build site_tests

package sitebdd

import (
	"testing"
	"time"
)

func TestScenarioWaveCoordinatorBlocksCurlUntilWaveReady(t *testing.T) {
	c := newScenarioWaveCoordinator()
	c.Reset([]ScenarioData{
		{Checkpoint: "alpha", WaveID: 1},
		{Checkpoint: "beta", WaveID: 1},
	}, "")

	waveA := c.Enter(1)
	waveB := c.Enter(1)

	released := make(chan struct{})
	go func() {
		c.WaitForCurlPhase(waveA)
		close(released)
	}()

	assertChannelBlocked(t, released, 100*time.Millisecond)

	c.MarkReady(waveA)
	assertChannelBlocked(t, released, 100*time.Millisecond)

	c.MarkReady(waveB)
	assertChannelReceives(t, released, 500*time.Millisecond)
}

func TestScenarioWaveCoordinatorBlocksNextWaveUntilCurrentWaveFinishes(t *testing.T) {
	c := newScenarioWaveCoordinator()
	c.Reset([]ScenarioData{
		{Checkpoint: "alpha", WaveID: 1},
		{Checkpoint: "beta", WaveID: 2},
	}, "")

	wave1 := c.Enter(1)

	admittedWave2 := make(chan *scenarioWave, 1)
	go func() {
		admittedWave2 <- c.Enter(2)
	}()

	assertChannelBlocked(t, admittedWave2, 100*time.Millisecond)

	c.MarkReady(wave1)
	c.Finish(wave1)

	select {
	case wave2 := <-admittedWave2:
		if wave2 == nil || wave2.id != 2 {
			t.Fatalf("expected wave 2 admission, got %#v", wave2)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for wave 2 admission")
	}
}

func TestAssignScenarioWavesSeparatesSharedCheckpoints(t *testing.T) {
	scenarios := []ScenarioData{
		{Checkpoint: "shared"},
		{Checkpoint: "other"},
		{Checkpoint: "shared"},
	}

	assignScenarioWaves(scenarios, 3)

	if scenarios[0].WaveID != 1 || scenarios[1].WaveID != 1 {
		t.Fatalf("expected first two scenarios in wave 1, got %d and %d", scenarios[0].WaveID, scenarios[1].WaveID)
	}
	if scenarios[2].WaveID != 2 {
		t.Fatalf("expected repeated checkpoint to move to wave 2, got %d", scenarios[2].WaveID)
	}
}

func assertChannelBlocked[T any](t *testing.T, ch <-chan T, timeout time.Duration) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("expected channel to remain blocked, got %#v", v)
	case <-time.After(timeout):
	}
}

func assertChannelReceives[T any](t *testing.T, ch <-chan T, timeout time.Duration) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(timeout):
		t.Fatal("timed out waiting for channel receive")
		var zero T
		return zero
	}
}
