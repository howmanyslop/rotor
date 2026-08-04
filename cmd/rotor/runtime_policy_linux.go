//go:build linux

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mackerelio/go-osstat/memory"
)

func effectiveMemory() int64 {
	host := fallbackMemory
	if stats, err := memory.Get(); err == nil && stats.Total > 0 {
		host = int64(stats.Total)
	}
	paths := []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"}
	if data, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		paths = append(paths, cgroupMemoryLimitPaths(string(data))...)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		limit, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err == nil && limit > 0 && limit < host {
			host = limit
		}
	}
	return host
}

func cgroupMemoryLimitPaths(cgroups string) []string {
	var paths []string
	for line := range strings.Lines(cgroups) {
		fields := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(fields) != 3 {
			continue
		}
		relative := strings.TrimPrefix(filepath.Clean(fields[2]), string(filepath.Separator))
		if fields[0] == "0" && fields[1] == "" {
			paths = append(paths, filepath.Join("/sys/fs/cgroup", relative, "memory.max"))
			continue
		}
		if slices.Contains(strings.Split(fields[1], ","), "memory") {
			paths = append(paths,
				filepath.Join("/sys/fs/cgroup/memory", relative, "memory.limit_in_bytes"),
				filepath.Join("/sys/fs/cgroup", relative, "memory.limit_in_bytes"),
			)
		}
	}
	return paths
}
