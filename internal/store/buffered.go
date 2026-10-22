package store

import (
	"context"
	"log/slog"
	"logline/internal/domain"
	"sync"
	"time"
)

type BufferedStore struct {
	store     *Store
	buffer    []domain.LogEntry
	mutex     sync.Mutex
	interval  time.Duration
	batchSize int
	done      chan struct{}
}

func NewBufferedStore(store *Store, batchSize int, interval time.Duration) *BufferedStore {
	bufferedStore := &BufferedStore{
		store:     store,
		buffer:    make([]domain.LogEntry, 0, batchSize),
		mutex:     sync.Mutex{},
		interval:  interval,
		batchSize: batchSize,
		done:      make(chan struct{}),
	}

	go bufferedStore.flushLoop()

	return bufferedStore
}

func (bs *BufferedStore) Add(entry domain.LogEntry) {
	bs.mutex.Lock()
	bs.buffer = append(bs.buffer, entry)
	full := len(bs.buffer) >= bs.batchSize
	bs.mutex.Unlock()

	if full {
		bs.flush()
	}
}

func (bs *BufferedStore) flushLoop() {
	ticker := time.NewTicker(bs.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bs.flush()
		case <-bs.done:
			bs.flush() // drain remaining entries on shutdown
			return
		}
	}
}

func (bs *BufferedStore) Close() {
	close(bs.done)
}

func (bs *BufferedStore) flush() {
	bs.mutex.Lock()
	if len(bs.buffer) == 0 {
		bs.mutex.Unlock()
		return
	}

	// swap: take the current buffer, replace it with a fresh one
	entries := bs.buffer
	bs.buffer = make([]domain.LogEntry, 0, bs.batchSize)
	bs.mutex.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := bs.store.InsertBatch(ctx, entries); err != nil {
		// todo: retry with backoff, or write to a local WAL file
		slog.Error("batch insert failed",
			slog.Int("entries", len(entries)),
			slog.String("error", err.Error()),
		)
	}
}
