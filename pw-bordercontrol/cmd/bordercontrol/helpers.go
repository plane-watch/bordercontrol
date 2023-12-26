package main

import "sync"

// goroutine management
type goRoutineManager struct {
	// common code to stop goroutines with infinite loops
	mu   sync.RWMutex // mutex used for locking/sync
	stop bool         // if set to true, then quit
}

func (g *goRoutineManager) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stop = true
}

func (g *goRoutineManager) CheckForStop() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.stop
}
