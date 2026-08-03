package main

import (
	"os"
	"runtime/debug"
)

const (
	fallbackMemory = int64(8 << 30)
	minimumLimit   = int64(512 << 20)
	maximumLimit   = int64(16 << 30)
)

func applyRuntimePolicy() {
	if _, set := os.LookupEnv("GOGC"); !set {
		debug.SetGCPercent(400)
	}
	if _, set := os.LookupEnv("GOMEMLIMIT"); !set {
		debug.SetMemoryLimit(automaticMemoryLimit(effectiveMemory()))
	}
}

func automaticMemoryLimit(memory int64) int64 {
	if memory <= 0 {
		memory = fallbackMemory
	}
	limit := memory - memory/4
	if limit < minimumLimit {
		return minimumLimit
	}
	if limit > maximumLimit {
		return maximumLimit
	}
	return limit
}
