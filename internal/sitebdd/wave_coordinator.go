//go:build site_tests

package sitebdd

import (
	"fmt"
	"sort"
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

	if siteDiagnosticsEnabled() {
		ids := make([]int, 0, len(c.waves))
		for id := range c.waves {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		summaries := make([]string, 0, len(ids))
		for _, id := range ids {
			summaries = append(summaries, fmt.Sprintf("%d:%d", id, c.waves[id].expected))
		}
		siteDiagnosticf("wave-reset filter=%q current=%d waves=%s", filter, c.currentWaveID, strings.Join(summaries, ","))
	}
}

func (c *scenarioWaveCoordinator) Enter(waveID int, scenarioKey string) *scenarioWave {
	if waveID < 1 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	waitLogged := false
	for {
		if c.currentWaveID == 0 {
			return nil
		}
		if waveID == c.currentWaveID {
			wave := c.waves[waveID]
			if wave != nil {
				wave.admitted++
				siteDiagnosticf(
					"wave-enter scenario=%q wave=%d admitted=%d/%d",
					scenarioKey,
					wave.id,
					wave.admitted,
					wave.expected,
				)
			}
			return wave
		}
		if !waitLogged {
			siteDiagnosticf(
				"wave-enter-wait scenario=%q target=%d current=%d",
				scenarioKey,
				waveID,
				c.currentWaveID,
			)
			waitLogged = true
		}
		c.cond.Wait()
	}
}

func (c *scenarioWaveCoordinator) MarkReady(wave *scenarioWave, scenarioKey string) {
	if wave == nil {
		return
	}
	c.mu.Lock()
	if wave.ready < wave.expected {
		wave.ready++
		siteDiagnosticf(
			"wave-ready scenario=%q wave=%d ready=%d/%d",
			scenarioKey,
			wave.id,
			wave.ready,
			wave.expected,
		)
		if wave.ready == wave.expected {
			wave.curlReleased = true
			siteDiagnosticf("wave-release wave=%d", wave.id)
			c.cond.Broadcast()
		}
	}
	c.mu.Unlock()
}

func (c *scenarioWaveCoordinator) WaitForCurlPhase(wave *scenarioWave, scenarioKey string) {
	if wave == nil {
		return
	}
	c.mu.Lock()
	waitLogged := false
	for !wave.curlReleased {
		if !waitLogged {
			siteDiagnosticf(
				"curl-wait scenario=%q wave=%d ready=%d/%d",
				scenarioKey,
				wave.id,
				wave.ready,
				wave.expected,
			)
			waitLogged = true
		}
		c.cond.Wait()
	}
	siteDiagnosticf("curl-release scenario=%q wave=%d", scenarioKey, wave.id)
	c.mu.Unlock()
}

func (c *scenarioWaveCoordinator) Finish(wave *scenarioWave, scenarioKey string) {
	if wave == nil {
		return
	}
	c.mu.Lock()
	if wave.finished < wave.expected {
		wave.finished++
		siteDiagnosticf(
			"wave-finish scenario=%q wave=%d finished=%d/%d",
			scenarioKey,
			wave.id,
			wave.finished,
			wave.expected,
		)
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
		siteDiagnosticf("wave-advance from=%d to=%d", wave.id, c.currentWaveID)
		c.cond.Broadcast()
	}
	c.mu.Unlock()
}
