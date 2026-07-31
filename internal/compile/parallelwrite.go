package compile

import (
	"os"
	"runtime"
	"strconv"
	"sync"
)

// writeWorkers returns the parallelism for the output write phases.
// ROTOR_WRITE_WORKERS overrides it (1 forces sequential writes — the
// pre-parallel baseline, useful for A/B timing on the same machine).
func writeWorkers() int {
	if v := os.Getenv("ROTOR_WRITE_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return n
		}
	}
	n := runtime.GOMAXPROCS(0)
	if n > 16 {
		n = 16
	}
	if n < 1 {
		n = 1
	}
	return n
}

// parallelize runs jobs on up to n workers. On the first job error the
// remaining queued jobs are skipped; already-running jobs finish. Returns
// the first error, or nil.
func parallelize(n int, jobs []func() error) error {
	if n <= 1 || len(jobs) <= 1 {
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
