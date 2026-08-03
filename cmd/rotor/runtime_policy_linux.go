//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"

	"github.com/mackerelio/go-osstat/memory"
)

func effectiveMemory() int64 {
	host := fallbackMemory
	if stats, err := memory.Get(); err == nil && stats.Total > 0 {
		host = int64(stats.Total)
	}
	for _, path := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		limit, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err == nil && limit > 0 && limit < host {
			return limit
		}
	}
	return host
}
