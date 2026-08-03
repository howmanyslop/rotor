package main

import "testing"

func TestMedian_returns_exact_fraction_for_odd_and_even_counts(t *testing.T) {
	tests := []struct {
		name      string
		durations []int64
		want      Fraction
	}{
		{
			name:      "odd count",
			durations: []int64{900, 100, 500},
			want:      Fraction{Numerator: 500, Denominator: 1},
		},
		{
			name:      "even count",
			durations: []int64{900, 100, 500, 300},
			want:      Fraction{Numerator: 800, Denominator: 2},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			durations := test.durations

			// When
			got, err := median(durations)

			// Then
			if err != nil {
				t.Fatalf("median: %v", err)
			}
			if got != test.want {
				t.Errorf("median(%v) = %#v, want %#v", durations, got, test.want)
			}
		})
	}
}

func TestNearestRankP95_returns_sorted_ceiling_rank(t *testing.T) {
	tests := []struct {
		name      string
		durations []int64
		want      int64
	}{
		{
			name:      "twenty samples selects nineteenth sorted value",
			durations: []int64{20, 1, 19, 2, 18, 3, 17, 4, 16, 5, 15, 6, 14, 7, 13, 8, 12, 9, 11, 10},
			want:      19,
		},
		{
			name:      "single sample selects the only value",
			durations: []int64{42},
			want:      42,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			durations := test.durations

			// When
			got, err := nearestRankP95(durations)

			// Then
			if err != nil {
				t.Fatalf("nearestRankP95: %v", err)
			}
			if got != test.want {
				t.Errorf("nearestRankP95(%v) = %d, want %d", durations, got, test.want)
			}
		})
	}
}
