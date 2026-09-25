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

func TestScenarioWaveCoordinatorAdmittedQuorumUnblocksNextWave(t *testing.T) {
	// Simulate a Go -run filter: 2 scenarios are in wave 1 (expected=2),
	// but only 1 is admitted (the other was filtered out by -run).
	// The admitted scenario should finish the wave and unblock wave 2.
	c := newScenarioWaveCoordinator()
	c.Reset([]ScenarioData{
		{Checkpoint: "alpha", WaveID: 1},
		{Checkpoint: "beta", WaveID: 1},
		{Checkpoint: "gamma", WaveID: 2},
	}, "")

	// Only alpha is admitted (simulating -run filter).
	wave1 := c.Enter(1)
	if wave1 == nil || wave1.id != 1 {
		t.Fatalf("expected wave 1, got %#v", wave1)
	}
	if wave1.admitted != 1 {
		t.Fatalf("expected admitted=1, got %d", wave1.admitted)
	}

	// gamma should be blocked waiting for wave 1 to finish.
	admittedWave2 := make(chan *scenarioWave, 1)
	go func() {
		admittedWave2 <- c.Enter(2)
	}()
	assertChannelBlocked(t, admittedWave2, 100*time.Millisecond)

	// alpha finishes wave 1 — admitted(1)==finished(1), wave should advance.
	c.MarkReady(wave1)
	c.Finish(wave1)

	select {
	case wave2 := <-admittedWave2:
		if wave2 == nil || wave2.id != 2 {
			t.Fatalf("expected wave 2 admission, got %#v", wave2)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for wave 2 admission after partial-wave finish")
	}
}

func TestScenarioWaveCoordinatorCancelUnblocksEnter(t *testing.T) {
	c := newScenarioWaveCoordinator()
	c.Reset([]ScenarioData{
		{Checkpoint: "alpha", WaveID: 1},
		{Checkpoint: "beta", WaveID: 2},
	}, "")

	// Enter wave 1 so current wave is 1.
	wave1 := c.Enter(1)

	// A goroutine tries to enter wave 2 — blocked because wave 1 hasn't finished.
	admittedWave2 := make(chan *scenarioWave, 1)
	go func() {
		admittedWave2 <- c.Enter(2)
	}()

	assertChannelBlocked(t, admittedWave2, 100*time.Millisecond)

	// Cancel the coordinator — Enter(2) should unblock and return nil.
	c.Cancel()

	select {
	case wave2 := <-admittedWave2:
		if wave2 != nil {
			t.Fatalf("expected nil wave after cancel, got %#v", wave2)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for Enter to unblock after Cancel")
	}

	// Subsequent Enter calls also return nil.
	if got := c.Enter(1); got != nil {
		t.Fatalf("expected nil from Enter after cancel, got %#v", got)
	}

	// Finish is a no-op after cancel (should not panic).
	c.Finish(wave1)
}

func TestScenarioWaveCoordinatorCancelUnblocksWaitForCurlPhase(t *testing.T) {
	c := newScenarioWaveCoordinator()
	c.Reset([]ScenarioData{
		{Checkpoint: "alpha", WaveID: 1},
		{Checkpoint: "beta", WaveID: 1},
	}, "")

	waveA := c.Enter(1)

	done := make(chan struct{})
	go func() {
		c.WaitForCurlPhase(waveA)
		close(done)
	}()

	assertChannelBlocked(t, done, 100*time.Millisecond)

	// Cancel unblocks WaitForCurlPhase even though MarkReady was never called.
	c.Cancel()
	assertChannelReceives(t, done, 500*time.Millisecond)
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
