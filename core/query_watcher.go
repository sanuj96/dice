package core

import (
	"context"
	"sync"
	"syscall"

	"github.com/charmbracelet/log"
)

// WatchEvent Event to notify clients about changes in query results due to key updates
type WatchEvent struct {
	Key       string
	Operation string
}

// WatchSubscription Event to watch/unwatch a query
type WatchSubscription struct {
	subscribe bool      // true for subscribe, false for unsubscribe
	query     DSQLQuery // query to watch
	clientFd  int       // client file descriptor
}

// WatchChan Channel to receive updates about keys that are being watched
var WatchChan chan WatchEvent

// WatchSubscriptionChan Channel to receive updates about query subscriptions
var WatchSubscriptionChan chan WatchSubscription

// QueryWatcher watches for changes in keys and notifies clients
type QueryWatcher struct {
	WatchList    sync.Map
	shardManager *ShardManager
}

// NewQueryWatcher initializes a new QueryWatcher
func NewQueryWatcher(shardManager *ShardManager) *QueryWatcher {
	return &QueryWatcher{
		WatchList:    sync.Map{},
		shardManager: shardManager,
	}
}

func (w *QueryWatcher) Run(ctx context.Context) {
	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		w.listenForSubscriptions(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		w.watchKeys(ctx)
	}()

	<-ctx.Done()
	wg.Wait()
}

// listenForSubscriptions listens for query subscriptions and unsubscribes
func (w *QueryWatcher) listenForSubscriptions(ctx context.Context) {
	for {
		select {
		case event := <-WatchSubscriptionChan:
			if event.subscribe {
				w.AddWatcher(event.query, event.clientFd)
			} else {
				w.RemoveWatcher(event.query, event.clientFd)
			}
		case <-ctx.Done():
			return
		}
	}
}

// watchKeys watches for changes in keys and notifies clients
func (w *QueryWatcher) watchKeys(ctx context.Context) {
	for {
		select {
		case event := <-WatchChan:
			w.WatchList.Range(func(key, value interface{}) bool {
				query := key.(DSQLQuery)
				clients := value.(*sync.Map)

				if WildCardMatch(query.KeyRegex, event.Key) {
					// TODO: Implement proper query execution on the shard.
					queryResult, err := ExecuteQuery(&query, w.shardManager.GetShard(0).store)
					if err != nil {
						log.Error(err)
						return true
					}

					encodedResult := Encode(CreatePushResponse(&query, &queryResult), false)
					clients.Range(func(clientKey, _ interface{}) bool {
						clientFd := clientKey.(int)
						_, err := syscall.Write(clientFd, encodedResult)
						if err != nil {
							w.RemoveWatcher(query, clientFd)
						}
						return true
					})
				}
				return true
			})
		case <-ctx.Done():
			return
		}
	}
}

// AddWatcher adds a client as a watcher to a query.
func (w *QueryWatcher) AddWatcher(query DSQLQuery, clientFd int) { //nolint:gocritic
	clients, _ := w.WatchList.LoadOrStore(query, &sync.Map{})
	clients.(*sync.Map).Store(clientFd, struct{}{})
}

// RemoveWatcher removes a client from the watchlist for a query.
func (w *QueryWatcher) RemoveWatcher(query DSQLQuery, clientFd int) { //nolint:gocritic
	if clients, ok := w.WatchList.Load(query); ok {
		clients.(*sync.Map).Delete(clientFd)
		// If no more clients for this query, remove the query from WatchList
		if w.clientCount(clients.(*sync.Map)) == 0 {
			w.WatchList.Delete(query)
		}
	}
}

// Helper function to count clients
func (w *QueryWatcher) clientCount(clients *sync.Map) int {
	count := 0
	clients.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}
