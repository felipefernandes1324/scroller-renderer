package judge

import (
	"math"
	"testing"

	"github.com/felipefernandes1324/scroller-renderer/internal/beatmap"
	"github.com/felipefernandes1324/scroller-renderer/internal/replay"
)

func TestSimulateLongNoteAddsSeparateTailJudgment(t *testing.T) {
	object := beatmap.HitObject{Time: 1000, End: 2000, Lane: 0, Long: true}
	events := []replay.Event{{Time: 1000, Mask: 1}, {Time: 2000, Mask: 0}}

	stats := Simulate([]beatmap.HitObject{object}, events, 8)

	if len(stats.AccuracyEvents) != 2 {
		t.Fatalf("got %d accuracy events, want head and tail", len(stats.AccuracyEvents))
	}
	if stats.AccuracyEvents[0].Grade != MAX || stats.AccuracyEvents[1].Grade != MAX {
		t.Fatalf("got head/tail grades %q/%q, want MAX/MAX", stats.AccuracyEvents[0].Grade, stats.AccuracyEvents[1].Grade)
	}
	if stats.Accuracy != 100 {
		t.Fatalf("got accuracy %.2f, want 100", stats.Accuracy)
	}
}

func TestSimulateLongNoteBreakCapsTailAt50(t *testing.T) {
	object := beatmap.HitObject{Time: 1000, End: 2000, Lane: 0, Long: true}
	events := []replay.Event{
		{Time: 1000, Mask: 1},
		{Time: 1500, Mask: 0},
		{Time: 1800, Mask: 1},
		{Time: 2000, Mask: 0},
	}

	stats := Simulate([]beatmap.HitObject{object}, events, 8)

	if got := stats.AccuracyEvents[1].Grade; got != Hit50 {
		t.Fatalf("got tail grade %q after breaking and re-grabbing, want Hit50", got)
	}
}

func TestApplyReplaySummaryDistributesAccuracyCorrection(t *testing.T) {
	stats := Stats{
		Results: []Result{{ScoreAfter: 100}},
		AccuracyEvents: []AccuracyEvent{
			{Time: 100, AccuracyAfter: 50},
			{Time: 200, AccuracyAfter: 80},
			{Time: 300, AccuracyAfter: 90},
		},
	}
	summary := replay.ScoreSummary{Count300: 1, CountMiss: 1, Score: 100, MaxCombo: 1}

	ApplyReplaySummary(&stats, summary)

	if got := stats.AccuracyEvents[2].AccuracyAfter; math.Abs(got-stats.Accuracy) > 1e-9 {
		t.Fatalf("last accuracy event is %.2f, want final accuracy %.2f", got, stats.Accuracy)
	}
	if stats.AccuracyEvents[0].AccuracyAfter == 50 {
		t.Fatal("expected summary correction to be distributed before the final event")
	}
}
