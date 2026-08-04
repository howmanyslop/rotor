//go:build linux

package main

import (
	"slices"
	"testing"
)

func TestCgroupMemoryLimitPaths(t *testing.T) {
	got := cgroupMemoryLimitPaths("0::/containers/job\n5:cpu,memory:/docker/task\n")
	want := []string{
		"/sys/fs/cgroup/containers/job/memory.max",
		"/sys/fs/cgroup/memory/docker/task/memory.limit_in_bytes",
		"/sys/fs/cgroup/docker/task/memory.limit_in_bytes",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}
