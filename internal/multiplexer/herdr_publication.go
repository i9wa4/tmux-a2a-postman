package multiplexer

import "sync"

var herdrPublicationMu sync.RWMutex

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
