package core

import (
	"github.com/dicedb/dice/config"
	"github.com/dicedb/dice/server/utils"
)

// Evicts the first key it found while iterating the map
// TODO: Make it efficient by doing thorough sampling
func evictFirst(store *Store) {
	withLocks(func() {
		store.store.All(func(k string, obj *Obj) bool {
			store.delByPtr(k)
			// stop after iterating over the first element
			return false
		})
	}, store, WithStoreLock())
}

// Randomly removes keys to make space for the new data added.
// The number of keys removed will be sufficient to free up at least 10% space
func evictAllkeysRandom(store *Store) {
	evictCount := int64(config.EvictionRatio * float64(config.KeysLimit))
	withLocks(func() {
		// Iteration of Golang dictionary can be considered as a random
		// because it depends on the hash of the inserted key
		store.store.All(func(k string, obj *Obj) bool {
			store.delByPtr(k)
			evictCount--
			// continue if evictCount > 0
			return evictCount > 0
		})
	}, store, WithStoreLock())
}

/*
 *  The approximated LRU algorithm
 */
func getCurrentClock() uint32 {
	return uint32(utils.GetCurrentTime().Unix()) & 0x00FFFFFF
}

func getIdleTime(lastAccessedAt uint32) uint32 {
	c := getCurrentClock()
	if c >= lastAccessedAt {
		return c - lastAccessedAt
	}
	return (0x00FFFFFF - lastAccessedAt) + c
}

func populateEvictionPool(store *Store) {
	sampleSize := 5

	// TODO: if we already have obj, why do we need to
	// look up in store.store again?
	withLocks(func() {
		store.store.All(func(k string, obj *Obj) bool {
			v, ok := store.store.Get(k)
			if ok {
				ePool.Push(k, v.LastAccessedAt)
				sampleSize--
			}
			// continue if sample size > 0
			// stop as soon as it hits 0
			return sampleSize > 0
		})
	}, store, WithStoreRLock())
}

// TODO: no need to populate everytime. should populate
// only when the number of keys to evict is less than what we have in the pool
func evictAllkeysLRU(store *Store) {
	populateEvictionPool(store)
	evictCount := int16(config.EvictionRatio * float64(config.KeysLimit))
	for i := 0; i < int(evictCount) && len(ePool.pool) > 0; i++ {
		item := ePool.Pop()
		if item == nil {
			return
		}
		store.DelByPtr(item.keyPtr)
	}
}

// TODO: implement LFU
func (store *Store) evict() {
	switch config.EvictionStrategy {
	case "simple-first":
		evictFirst(store)
	case "allkeys-random":
		evictAllkeysRandom(store)
	case "allkeys-lru":
		evictAllkeysLRU(store)
	}
}
