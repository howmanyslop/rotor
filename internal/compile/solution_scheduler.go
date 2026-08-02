package compile

import (
	"sync"
	"sync/atomic"
)

type solutionTask struct {
	index        int
	predecessors []int
	waitOnly     []int
}

// RunSolutionTasks runs each task once after all of its dependency barriers
// close. A task error is recorded at that task's index and does not affect any
// other task.
func RunSolutionTasks(tasks []solutionTask, builders int, run func(index int) error) []error {
	if builders <= 0 {
		builders = 4
	}

	done := make(map[int]chan struct{}, len(tasks))
	outcomeLength := 0
	for _, task := range tasks {
		done[task.index] = make(chan struct{})
		if task.index >= outcomeLength {
			outcomeLength = task.index + 1
		}
	}
	outcomes := make([]error, outcomeLength)
	executed := make([]atomic.Bool, len(tasks))

	var next atomic.Int64
	var workers sync.WaitGroup
	workers.Add(builders)
	for range builders {
		go func() {
			defer workers.Done()
			for {
				position := int(next.Add(1) - 1)
				if position >= len(tasks) {
					return
				}
				task := tasks[position]
				executed[position].Store(true)
				for _, predecessor := range task.predecessors {
					<-done[predecessor]
				}
				for _, predecessor := range task.waitOnly {
					<-done[predecessor]
				}
				outcomes[task.index] = run(task.index)
				close(done[task.index])
			}
		}()
	}
	workers.Wait()

	for position, task := range tasks {
		if !executed[position].Load() {
			close(done[task.index])
		}
	}
	return outcomes
}
