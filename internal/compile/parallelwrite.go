package compile

import (
	"math"
	"os"
	"strconv"
	"sync"
)

const (
	writeConcurrencyCap     = 256
	writeConcurrencyDefault = 8
	writeSyncCutoff         = 16
)

func writeWorkers() int {
	if n, ok := parseWriteConcurrency(os.Getenv("RBXTSC_WRITE_CONCURRENCY")); ok {
		return n
	}
	if n, ok := parseWriteConcurrency(os.Getenv("ROTOR_WRITE_WORKERS")); ok {
		return n
	}
	return writeConcurrencyDefault
}

func parseWriteConcurrency(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	n = math.Floor(n)
	if n < 1 {
		return 0, false
	}
	if n >= writeConcurrencyCap {
		return writeConcurrencyCap, true
	}
	return int(n), true
}

// parallelize runs jobs on up to n workers. On the first job error the
// remaining queued jobs are skipped; already-running jobs finish. Returns
// the first error, or nil.
func parallelize(n int, jobs []func() error) error {
	if n > writeConcurrencyCap {
		n = writeConcurrencyCap
	}
	if n <= 1 || len(jobs) < writeSyncCutoff {
		for _, job := range jobs {
			if err := job(); err != nil {
				return err
			}
		}
		return nil
	}
	var (
		mu      sync.Mutex
		first   error
		started sync.WaitGroup
	)
	ch := make(chan func() error)
	started.Add(n)
	for range n {
		go func() {
			defer started.Done()
			for job := range ch {
				mu.Lock()
				failed := first != nil
				mu.Unlock()
				if failed {
					continue
				}
				if err := job(); err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	for _, job := range jobs {
		ch <- job
	}
	close(ch)
	started.Wait()
	return first
}
