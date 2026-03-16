//go:build site_tests

package sitebdd

import (
	"strings"
	"sync"
)

var globalScenarioWaveCoordinator = newScenarioWaveCoordinator()

type scenarioWaveCoordinator struct {
	mu            sync.Mutex
	cond          *sync.Cond
	currentWaveID int
	waves         map[int]*scenarioWave
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

func (c *scenarioWaveCoordinator) Enter(waveID int) *scenarioWave {
	if waveID < 1 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if c.currentWaveID == 0 {
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
	if wave.ready < wave.expected {
		wave.ready++
		if wave.ready == wave.expected {
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
	for !wave.curlReleased {
		c.cond.Wait()
	}
	c.mu.Unlock()
}

func (c *scenarioWaveCoordinator) Finish(wave *scenarioWave) {
	if wave == nil {
		return
	}
	c.mu.Lock()
	if wave.finished < wave.expected {
		wave.finished++
	}
	if wave.finished == wave.expected && c.currentWaveID == wave.id {
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
