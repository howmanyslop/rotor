package main

import (
	"errors"
	"math/big"
	"slices"
)

var ErrEmptySamples = errors.New("empty sample set")

type Fraction struct {
	Numerator   int64 `json:"numerator"`
	Denominator int64 `json:"denominator"`
}

func median(durations []int64) (Fraction, error) {
	if len(durations) == 0 {
		return Fraction{}, ErrEmptySamples
	}
	ordered := slices.Clone(durations)
	slices.Sort(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 != 0 {
		return Fraction{Numerator: ordered[middle], Denominator: 1}, nil
	}
	return Fraction{Numerator: ordered[middle-1] + ordered[middle], Denominator: 2}, nil
}

func nearestRankP95(durations []int64) (int64, error) {
	if len(durations) == 0 {
		return 0, ErrEmptySamples
	}
	ordered := slices.Clone(durations)
	slices.Sort(ordered)
	rank := (95*len(ordered) + 99) / 100
	return ordered[rank-1], nil
}

func ratio(numerator, denominator Fraction) Fraction {
	n := big.NewInt(numerator.Numerator)
	n.Mul(n, big.NewInt(denominator.Denominator))
	d := big.NewInt(numerator.Denominator)
	d.Mul(d, big.NewInt(denominator.Numerator))
	gcd := new(big.Int).GCD(nil, nil, n, d)
	n.Quo(n, gcd)
	d.Quo(d, gcd)
	return Fraction{Numerator: n.Int64(), Denominator: d.Int64()}
}

func exceeds(value, limit Fraction) bool {
	left := big.NewInt(value.Numerator)
	left.Mul(left, big.NewInt(limit.Denominator))
	right := big.NewInt(limit.Numerator)
	right.Mul(right, big.NewInt(value.Denominator))
	return left.Cmp(right) > 0
}
