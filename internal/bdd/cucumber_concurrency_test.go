package bdd

import "runtime"

func bddScenarioConcurrency() int {
	if n := runtime.NumCPU(); n > 0 {
		return n
	}
	return 1
}
