package feedproxy

func isInitialised() bool {
	initialisedMu.RLock()
	defer initialisedMu.RUnlock()
	return initialised
}
