package main

import (
	"math"
	"testing"
)

func TestComputeIntentCost(t *testing.T) {
	tests := []struct {
		name                                     string
		input, output, cacheRead, cacheWrite      int64
		want                                     float64
	}{
		{"all zeros", 0, 0, 0, 0, 0.0},
		{"only input tokens", 1000, 0, 0, 0, 1000 * haikuPriceInputPerToken},
		{"mixed tokens", 500, 100, 200, 50,
			500*haikuPriceInputPerToken +
				100*haikuPriceOutputPerToken +
				200*haikuPriceCacheReadPerToken +
				50*haikuPriceCacheWritePerToken,
		},
		{"large counts no overflow", 1_000_000, 500_000, 2_000_000, 100_000,
			1_000_000*haikuPriceInputPerToken +
				500_000*haikuPriceOutputPerToken +
				2_000_000*haikuPriceCacheReadPerToken +
				100_000*haikuPriceCacheWritePerToken,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeIntentCost(tt.input, tt.output, tt.cacheRead, tt.cacheWrite)
			if math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("computeIntentCost(%d,%d,%d,%d) = %g, want %g", tt.input, tt.output, tt.cacheRead, tt.cacheWrite, got, tt.want)
			}
		})
	}
}
