//go:build !linux

package main

import "github.com/mackerelio/go-osstat/memory"

func effectiveMemory() int64 {
	stats, err := memory.Get()
	if err != nil || stats.Total == 0 {
		return fallbackMemory
	}
	return int64(stats.Total)
}
