//go:build site_tests

package sitebdd

import "sync"

var globalScenarioWaveCoordinator = newScenarioWaveCoordinator()

type scenarioWaveCoordinator struct {
	mu          sync.Mutex
	cond        *sync.Cond
	current     *scenarioWave
	remaining   int
	maxWaveSize int
	nextWaveID  int
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

func (c *scenarioWaveCoordinator) Reset(totalScenarios, maxWaveSize int) {
	if maxWaveSize < 1 {
		maxWaveSize = 1
	}
	c.mu.Lock()
	c.current = nil
	c.remaining = totalScenarios
	c.maxWaveSize = maxWaveSize
	c.nextWaveID = 1
	c.mu.Unlock()
}

func (c *scenarioWaveCoordinator) Enter() *scenarioWave {
	c.mu.Lock()
	defer c.mu.Unlock()

	for {
		if c.current == nil {
			expected := c.maxWaveSize
			if c.remaining < expected {
				expected = c.remaining
			}
			c.current = &scenarioWave{
				id:       c.nextWaveID,
				expected: expected,
			}
			c.nextWaveID++
		}

		if c.current.admitted < c.current.expected {
			wave := c.current
			wave.admitted++
			c.remaining--
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
	if wave.finished == wave.expected && c.current == wave {
		c.current = nil
		c.cond.Broadcast()
	}
	c.mu.Unlock()
}
