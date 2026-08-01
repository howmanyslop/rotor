package main

import (
	"fmt"
	"strconv"
)

func parsePositiveIntFlag(name, value string) (*int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return nil, fmt.Errorf("invalid --%s value %q (must be a positive integer)", name, value)
	}
	return &n, nil
}

func isNumericFlagValue(value string) bool {
	if value == "" || value[0] != '-' {
		return value != ""
	}
	return len(value) > 1 && value[1] >= '0' && value[1] <= '9'
}
