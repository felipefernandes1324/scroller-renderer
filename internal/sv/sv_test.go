package sv

import (
	"math"
	"testing"

	"github.com/felipefernandes1324/scroller-renderer/internal/beatmap"
)

func TestPositionIntegratesSVChangesIncludingZeroAndNegative(t *testing.T) {
	timeline := New([]beatmap.TimingPoint{
		{Time: 0, BeatLength: 500, Uninherited: true},
		{Time: 500, BeatLength: -50},
		{Time: 750, BeatLength: 0},
		{Time: 1000, BeatLength: 100},
	})
	for _, test := range []struct {
		time, want float64
	}{
		{250, 250},
		{625, 750},
		{900, 1000},
		{1100, 900},
	} {
		if got := timeline.Position(test.time); math.Abs(got-test.want) > 1e-9 {
			t.Errorf("Position(%v) = %v, want %v", test.time, got, test.want)
		}
	}
}
