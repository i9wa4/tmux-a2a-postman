package multiplexer

import "sync"

var (
	herdrPublicationMu         sync.RWMutex
	herdrPublicationGeneration uint64
)

func LockHerdrPublicationRead() {
	herdrPublicationMu.RLock()
}

func UnlockHerdrPublicationRead() {
	herdrPublicationMu.RUnlock()
}

func LockHerdrPublicationWrite() {
	herdrPublicationMu.Lock()
}

func UnlockHerdrPublicationWrite() {
	herdrPublicationMu.Unlock()
}

func HerdrPublicationGenerationLocked() uint64 {
	return herdrPublicationGeneration
}

func AdvanceHerdrPublicationGenerationLocked() uint64 {
	herdrPublicationGeneration++
	return herdrPublicationGeneration
}
