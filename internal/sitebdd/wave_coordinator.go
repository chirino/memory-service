//go:build site_tests

package sitebdd

import (
	"strings"
	"sync"
)

var globalScenarioWaveCoordinator = newScenarioWaveCoordinator()

// scenarioWaveCoordinator admits scenarios in waves of up to
// siteScenarioConcurrency() members (preassigned as @wave_N tags by
// assignScenarioWaves). Wave members build and start their checkpoints
// concurrently; the first curl step of each member waits until every admitted
// member is running or has exited, and the next wave cannot start building
// until the current wave drains. This keeps curl traffic from overlapping
// checkpoint build/start work.
type scenarioWaveCoordinator struct {
	mu            sync.Mutex
	cond          *sync.Cond
	currentWaveID int
	waves         map[int]*scenarioWave
	cancelled     bool
}

type scenarioWave struct {
	id           int
	expected     int
	admitted     int
	ready        int
	finished     int
	curlReleased bool
}

func newScenarioWaveCoordinator() *scenarioWaveCoordinator {
	c := &scenarioWaveCoordinator{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *scenarioWaveCoordinator) Reset(scenarios []ScenarioData, filter string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.currentWaveID = 0
	c.waves = map[int]*scenarioWave{}
	c.cancelled = false

	filter = strings.TrimSpace(filter)
	var expr tagExpr
	var err error
	if filter != "" {
		expr, err = parseTagFilter(filter)
		if err != nil {
			return
		}
	}

	for _, scenario := range scenarios {
		if scenario.WaveID < 1 {
			continue
		}
		if expr != nil {
			tagSet := make(map[string]struct{}, len(deriveTags(scenario)))
			for _, tag := range deriveTags(scenario) {
				tagSet[strings.TrimPrefix(tag, "@")] = struct{}{}
			}
			if !expr.eval(tagSet) {
				continue
			}
		}
		wave := c.waves[scenario.WaveID]
		if wave == nil {
			wave = &scenarioWave{id: scenario.WaveID}
			c.waves[scenario.WaveID] = wave
		}
		wave.expected++
		if c.currentWaveID == 0 || scenario.WaveID < c.currentWaveID {
			c.currentWaveID = scenario.WaveID
		}
	}

}

// Cancel unblocks all goroutines waiting in Enter or WaitForCurlPhase and
// causes subsequent Enter calls to return nil immediately. It is safe to call
// from a t.Cleanup so the coordinator is always drained when a test suite
// exits (including on timeout or early failure).
func (c *scenarioWaveCoordinator) Cancel() {
	c.mu.Lock()
	c.cancelled = true
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *scenarioWaveCoordinator) Enter(waveID int) *scenarioWave {
	if waveID < 1 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if c.cancelled || c.currentWaveID == 0 {
			return nil
		}
		if waveID == c.currentWaveID {
			wave := c.waves[waveID]
			if wave != nil {
				wave.admitted++
			}
			return wave
		}
		c.cond.Wait()
	}
}

func (c *scenarioWaveCoordinator) MarkReady(wave *scenarioWave) {
	if wave == nil {
		return
	}
	c.mu.Lock()
	if wave.ready < wave.admitted {
		wave.ready++
		if wave.ready == wave.admitted {
			wave.curlReleased = true
			c.cond.Broadcast()
		}
	}
	c.mu.Unlock()
}

func (c *scenarioWaveCoordinator) WaitForCurlPhase(wave *scenarioWave) {
	if wave == nil {
		return
	}
	c.mu.Lock()
	for !wave.curlReleased && !c.cancelled {
		c.cond.Wait()
	}
	c.mu.Unlock()
}

func (c *scenarioWaveCoordinator) Finish(wave *scenarioWave) {
	if wave == nil {
		return
	}
	c.mu.Lock()
	if wave.finished < wave.admitted {
		wave.finished++
	}
	// Advance once all admitted scenarios have finished. Using admitted (not
	// expected) means a Go -run filter that schedules fewer scenarios than
	// expected still lets the wave complete instead of deadlocking.
	if wave.admitted > 0 && wave.finished == wave.admitted && c.currentWaveID == wave.id {
		delete(c.waves, wave.id)
		c.currentWaveID = 0
		for id := wave.id + 1; len(c.waves) > 0; id++ {
			if _, ok := c.waves[id]; ok {
				c.currentWaveID = id
				break
			}
		}
		c.cond.Broadcast()
	}
	c.mu.Unlock()
}
