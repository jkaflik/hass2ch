package ingestion

import (
	"sync"
	"time"
)

type Partitioner[T any] func(T) (string, error)

type BatchOptions[T any] struct {
	// MaxSize is the maximum number of items to batch together.
	MaxSize int

	// MaxWait is the maximum amount of time to wait before sending a batch.
	MaxWait time.Duration

	// PartitionBy is a function that returns a string key to partition the batch.
	// It can be used to batch items together that share a common key.
	// If PartitionBy is nil, all items are batched together.
	PartitionBy Partitioner[T]
}

func (o *BatchOptions[T]) defaults() {
	if o.MaxSize == 0 {
		o.MaxSize = 100
	}

	if o.MaxWait == 0 {
		o.MaxWait = 60 * time.Second
	}
}

func Batch[T any](in chan T, opts BatchOptions[T]) (chan []T, chan error) {
	opts.defaults()

	out := make(chan []T)
	errc := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errc)
		var batches map[string][]T
		var batchesMtx sync.Mutex
		var timers map[string]*time.Timer
		for {
			select {
			case item, ok := <-in:
				if !ok {
					batchesMtx.Lock()
					for _, batch := range batches {
						out <- batch
					}
					return
				}

				key := ""
				if opts.PartitionBy != nil {
					var err error
					key, err = opts.PartitionBy(item)
					if err != nil {
						errc <- err
						continue
					}
				}

				batchesMtx.Lock()

				if batches == nil {
					batches = make(map[string][]T)
					timers = make(map[string]*time.Timer)
				}

				if _, ok := batches[key]; !ok {
					batches[key] = []T{item}
					timers[key] = time.AfterFunc(opts.MaxWait, func() {
						batchesMtx.Lock()
						defer batchesMtx.Unlock()
						out <- batches[key]
						delete(batches, key)
					})
				} else {
					batches[key] = append(batches[key], item)
					if len(batches[key]) == opts.MaxSize {
						timers[key].Stop()
						out <- batches[key]
						delete(batches, key)
					}
				}

				batchesMtx.Unlock()
			}
		}
	}()
	return out, errc
}
