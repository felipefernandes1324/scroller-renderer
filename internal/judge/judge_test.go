package judge

import (
	"testing"

	"github.com/felipefernandes1324/scroller-renderer/internal/beatmap"
	"github.com/felipefernandes1324/scroller-renderer/internal/replay"
)

func TestAutoReplayJudgesLongNoteAndMaintainsCombo(t *testing.T) {
	objects := []beatmap.HitObject{
		{Time: 1000, End: 1000, Lane: 0},
		{Time: 1500, End: 1900, Lane: 1, Long: true},
	}
	stats := Simulate(objects, replay.Auto(objects, 4), 5)
	if len(stats.Results) != 2 || stats.Results[0].Grade != MAX || stats.Results[1].Grade != MAX {
		t.Fatalf("unexpected results: %+v", stats.Results)
	}
	if stats.MaxCombo != 2 || stats.Accuracy != 100 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestMissBreaksCombo(t *testing.T) {
	objects := []beatmap.HitObject{{Time: 1000, Lane: 0}}
	stats := Simulate(objects, nil, 5)
	if stats.Results[0].Grade != Miss || stats.Combo != 0 || stats.Accuracy != 0 {
		t.Fatalf("unexpected miss stats: %+v", stats)
	}
}
