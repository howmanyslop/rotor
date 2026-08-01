package compile

import (
	"errors"
	"testing"
)

func TestSolutionSchedulerSerializesBuildersOne(t *testing.T) {
	tasks := indexedSolutionTasks(3)
	started := make(chan int, len(tasks))
	finished := make(chan int, len(tasks))
	release := make(chan struct{})
	runDone := make(chan []error, 1)

	go func() {
		runDone <- RunSolutionTasks(tasks, 1, func(index int) error {
			started <- index
			<-release
			finished <- index
			return nil
		})
	}()

	for expected := range len(tasks) {
		if got := <-started; got != expected {
			t.Fatalf("started task %d, want %d", got, expected)
		}
		assertNoTaskSignal(t, started)
		release <- struct{}{}
		if got := <-finished; got != expected {
			t.Fatalf("finished task %d, want %d", got, expected)
		}
	}

	assertNoErrors(t, <-runDone)
}

func TestSolutionSchedulerRunsIndependentTasksConcurrently(t *testing.T) {
	tasks := indexedSolutionTasks(2)
	started := make(chan int, len(tasks))
	release := make(chan struct{})
	runDone := make(chan []error, 1)

	go func() {
		runDone <- RunSolutionTasks(tasks, 2, func(index int) error {
			started <- index
			<-release
			return nil
		})
	}()

	assertTaskIndexes(t, started, len(tasks))
	close(release)
	assertNoErrors(t, <-runDone)
}

func TestSolutionSchedulerWaitsForAllPredecessors(t *testing.T) {
	tasks := []solutionTask{
		{index: 0},
		{index: 1},
		{index: 2, predecessors: []int{0, 1}},
	}
	started := make(chan int, len(tasks))
	finished := make(chan int, len(tasks))
	release := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	dependentStarted := make(chan struct{}, 1)
	runDone := make(chan []error, 1)

	go func() {
		runDone <- RunSolutionTasks(tasks, 3, func(index int) error {
			switch index {
			case 0, 1:
				started <- index
				<-release[index]
				finished <- index
			default:
				dependentStarted <- struct{}{}
			}
			return nil
		})
	}()

	assertTaskIndexes(t, started, 2)
	assertNoSignal(t, dependentStarted)

	release[0] <- struct{}{}
	if got := <-finished; got != 0 {
		t.Fatalf("finished predecessor %d, want 0", got)
	}
	assertNoSignal(t, dependentStarted)

	release[1] <- struct{}{}
	if got := <-finished; got != 1 {
		t.Fatalf("finished predecessor %d, want 1", got)
	}
	<-dependentStarted

	assertNoErrors(t, <-runDone)
}

func TestSolutionSchedulerWaitOnlyDoesNotPropagateFailure(t *testing.T) {
	failure := errors.New("wait-only task failed")
	tasks := []solutionTask{{index: 0}, {index: 1, waitOnly: []int{0}}}
	started := make(chan int, len(tasks))
	finished := make(chan int, len(tasks))
	dependentStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	runDone := make(chan []error, 1)

	go func() {
		runDone <- RunSolutionTasks(tasks, 2, func(index int) error {
			if index == 0 {
				started <- index
				<-release
				finished <- index
				return failure
			}
			dependentStarted <- struct{}{}
			return nil
		})
	}()

	if got := <-started; got != 0 {
		t.Fatalf("started task %d, want 0", got)
	}
	assertNoSignal(t, dependentStarted)
	release <- struct{}{}
	if got := <-finished; got != 0 {
		t.Fatalf("finished task %d, want 0", got)
	}
	<-dependentStarted

	outcomes := <-runDone
	if !errors.Is(outcomes[0], failure) {
		t.Fatalf("task 0 error = %v, want %v", outcomes[0], failure)
	}
	if outcomes[1] != nil {
		t.Fatalf("task 1 error = %v, want nil", outcomes[1])
	}
}

func TestSolutionSchedulerRunsUnrelatedTasksAfterFailure(t *testing.T) {
	failure := errors.New("task failed")
	tasks := indexedSolutionTasks(2)
	failed := make(chan struct{}, 1)
	unrelatedStarted := make(chan struct{}, 1)
	runDone := make(chan []error, 1)

	go func() {
		runDone <- RunSolutionTasks(tasks, 2, func(index int) error {
			if index == 0 {
				failed <- struct{}{}
				return failure
			}
			unrelatedStarted <- struct{}{}
			return nil
		})
	}()

	<-failed
	<-unrelatedStarted
	outcomes := <-runDone
	if !errors.Is(outcomes[0], failure) {
		t.Fatalf("task 0 error = %v, want %v", outcomes[0], failure)
	}
	if outcomes[1] != nil {
		t.Fatalf("task 1 error = %v, want nil", outcomes[1])
	}
}

func TestSolutionSchedulerJoinsBlockedTasksBeforeReturning(t *testing.T) {
	tasks := []solutionTask{{index: 0}}
	started := make(chan struct{}, 1)
	finished := make(chan struct{}, 1)
	release := make(chan struct{})
	runDone := make(chan []error, 1)

	go func() {
		runDone <- RunSolutionTasks(tasks, 1, func(index int) error {
			started <- struct{}{}
			<-release
			finished <- struct{}{}
			return nil
		})
	}()

	<-started
	select {
	case <-runDone:
		t.Fatal("RunSolutionTasks returned while a task was blocked")
	default:
	}

	close(release)
	<-finished
	assertNoErrors(t, <-runDone)
}

func TestSolutionSchedulerDefaultsToFourBuilders(t *testing.T) {
	for _, test := range []struct {
		name     string
		builders int
	}{
		{name: "zero", builders: 0},
		{name: "negative", builders: -2},
	} {
		t.Run(test.name, func(t *testing.T) {
			tasks := indexedSolutionTasks(4)
			started := make(chan int, len(tasks))
			release := make(chan struct{})
			runDone := make(chan []error, 1)

			go func() {
				runDone <- RunSolutionTasks(tasks, test.builders, func(index int) error {
					started <- index
					<-release
					return nil
				})
			}()

			assertTaskIndexes(t, started, len(tasks))
			close(release)
			assertNoErrors(t, <-runDone)
		})
	}
}

func TestSolutionSchedulerClosesEveryDoneBarrier(t *testing.T) {
	tasks := []solutionTask{
		{index: 0},
		{index: 1},
		{index: 2, predecessors: []int{0}, waitOnly: []int{1}},
	}
	started := make(chan int, len(tasks))

	outcomes := RunSolutionTasks(tasks, 3, func(index int) error {
		started <- index
		return nil
	})

	assertTaskIndexes(t, started, len(tasks))
	assertNoErrors(t, outcomes)
}

func assertNoTaskSignal(t *testing.T, signal <-chan int) {
	select {
	case index := <-signal:
		t.Fatalf("unexpected task signal for index %d", index)
	default:
	}
}

func indexedSolutionTasks(count int) []solutionTask {
	tasks := make([]solutionTask, count)
	for index := range tasks {
		tasks[index].index = index
	}
	return tasks
}

func assertTaskIndexes(t *testing.T, signal <-chan int, count int) {
	seen := make([]bool, count)
	for range count {
		index := <-signal
		if index < 0 || index >= len(seen) || seen[index] {
			t.Fatalf("unexpected task index %d", index)
		}
		seen[index] = true
	}
}

func assertNoSignal(t *testing.T, signal <-chan struct{}) {
	select {
	case <-signal:
		t.Fatal("unexpected signal")
	default:
	}
}

func assertNoErrors(t *testing.T, outcomes []error) {
	for index, err := range outcomes {
		if err != nil {
			t.Errorf("task %d error = %v, want nil", index, err)
		}
	}
}
