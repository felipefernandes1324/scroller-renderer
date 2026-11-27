package judge

import (
	"math"
	"sort"

	"github.com/felipefernandes1324/scroller-renderer/internal/beatmap"
	"github.com/felipefernandes1324/scroller-renderer/internal/replay"
)

type Grade string

const (
	MAX    Grade = "mania-hit300g"
	Hit300 Grade = "mania-hit300"
	Hit200 Grade = "mania-hit200"
	Hit100 Grade = "mania-hit100"
	Hit50  Grade = "mania-hit50"
	Miss   Grade = "mania-hit0"
)

type Result struct {
	Time          float64
	EndTime       float64
	HitTime       float64
	ReleaseTime   float64
	HitError      float64
	Lane          int
	Grade         Grade
	Long          bool
	Sound         int
	Sample        string
	ComboAfter    int
	AccuracyAfter float64
	ScoreAfter    int
	HPAfter       float64
}

type TimingWindows struct {
	W320 float64
	W300 float64
	W200 float64
	W100 float64
	W50  float64
	Miss float64
}

func Windows(od float64) TimingWindows {
	w320 := math.Max(8, 16.5-0.5*od)
	w300 := math.Max(w320, 64-3*od)
	w200 := math.Max(w300, 97-3*od)
	w100 := math.Max(w200, 127-3*od)
	w50 := math.Max(w100, 151-3*od)
	miss := math.Max(w50, 188-3*od)
	return TimingWindows{
		W320: w320,
		W300: w300,
		W200: w200,
		W100: w100,
		W50:  w50,
		Miss: miss,
	}
}

type Stats struct {
	Combo    int
	MaxCombo int
	Accuracy float64
	Results  []Result
}

type keyEvent struct {
	time float64
	down bool
	used bool
}

func Simulate(objects []beatmap.HitObject, replayEvents []replay.Event, od float64) Stats {
	win := Windows(od)
	w320 := win.W320
	w300 := win.W300
	w200 := win.W200
	w100 := win.W100
	w50 := win.W50
	missWindow := win.Miss

	keysByLane := make(map[int][]keyEvent)
	var previous uint32
	for _, event := range replayEvents {
		changedDown := event.Mask &^ previous
		changedUp := previous &^ event.Mask
		for lane := 0; lane < 32; lane++ {
			bit := uint32(1) << lane
			if changedDown&bit != 0 {
				keysByLane[lane] = append(keysByLane[lane], keyEvent{time: event.Time, down: true})
			}
			if changedUp&bit != 0 {
				keysByLane[lane] = append(keysByLane[lane], keyEvent{time: event.Time})
			}
		}
		previous = event.Mask
	}
	for lane := range keysByLane {
		sort.SliceStable(keysByLane[lane], func(i, j int) bool {
			return keysByLane[lane][i].time < keysByLane[lane][j].time
		})
	}
	sortedObjects := append([]beatmap.HitObject(nil), objects...)
	sort.SliceStable(sortedObjects, func(i, j int) bool { return sortedObjects[i].Time < sortedObjects[j].Time })

	stats := Stats{}
	total, maxPoints := 0.0, float64(len(sortedObjects))*320
	totalAccuracy, maxAccuracyPoints := 0.0, float64(len(sortedObjects))*300
	hp := 1.0
	cursors := make(map[int]int)
	for objectIndex, object := range sortedObjects {
		keys := keysByLane[object.Lane]
		cursor := cursors[object.Lane]
		for cursor < len(keys) && keys[cursor].time < object.Time-missWindow {
			cursor++
		}
		cursors[object.Lane] = cursor
		best := -1
		bestDelta := math.Inf(1)
		for i := cursor; i < len(keys) && keys[i].time <= object.Time+missWindow; i++ {
			if keys[i].used || !keys[i].down {
				continue
			}
			delta := math.Abs(keys[i].time - object.Time)
			if delta < bestDelta {
				best, bestDelta = i, delta
			}
		}
		grade := Miss
		hitTime := object.Time + missWindow
		releaseTime := object.End
		hitError := 0.0
		if best >= 0 {
			keys[best].used = true
			hitTime = keys[best].time
			hitError = hitTime - object.Time
			switch {
			case bestDelta <= w320:
				grade = MAX
			case bestDelta <= w300:
				grade = Hit300
			case bestDelta <= w200:
				grade = Hit200
			case bestDelta <= w100:
				grade = Hit100
			case bestDelta <= w50:
				grade = Hit50
			}
			if object.Long && grade != Miss {
				released := false
				releaseStart := sort.Search(len(keys), func(i int) bool { return keys[i].time >= object.End-w50 })
				for i := releaseStart; i < len(keys) && keys[i].time <= object.End+missWindow; i++ {
					if !keys[i].down {
						released = true
						releaseTime = keys[i].time
						break
					}
				}
				if !released {
					grade = Miss
					releaseTime = object.End
				}
			}
		}
		value := gradeValue(grade)
		total += value
		totalAccuracy += accuracyValue(grade)
		if grade == Miss {
			stats.Combo = 0
			hp = math.Max(0.05, hp-0.08)
		} else {
			stats.Combo++
			if stats.Combo > stats.MaxCombo {
				stats.MaxCombo = stats.Combo
			}
			hp = math.Min(1.0, hp+0.025)
		}
		scoreAfter := 0
		if maxPoints > 0 {
			scoreAfter = int(math.Round(total / maxPoints * 1000000))
		}
		stats.Results = append(stats.Results, Result{
			Time: object.Time, EndTime: object.End, HitTime: hitTime, ReleaseTime: releaseTime,
			HitError: hitError, Lane: object.Lane, Grade: grade, Long: object.Long,
			Sound: object.Sound, Sample: object.Sample, ComboAfter: stats.Combo,
			AccuracyAfter: totalAccuracy / (float64(objectIndex+1) * 300) * 100,
			ScoreAfter:    scoreAfter, HPAfter: hp,
		})
	}
	if maxAccuracyPoints > 0 {
		stats.Accuracy = totalAccuracy / maxAccuracyPoints * 100
	}
	return stats
}

func ApplyReplaySummary(stats *Stats, summary replay.ScoreSummary) {
	stats.Accuracy = summary.Accuracy()
	stats.MaxCombo = summary.MaxCombo
	if len(stats.Results) == 0 {
		return
	}
	last := &stats.Results[len(stats.Results)-1]
	last.AccuracyAfter = stats.Accuracy
	last.ScoreAfter = summary.Score
}

func gradeValue(grade Grade) float64 {
	switch grade {
	case MAX:
		return 320
	case Hit300:
		return 300
	case Hit200:
		return 200
	case Hit100:
		return 100
	case Hit50:
		return 50
	default:
		return 0
	}
}

func accuracyValue(grade Grade) float64 {
	switch grade {
	case MAX, Hit300:
		return 300
	case Hit200:
		return 200
	case Hit100:
		return 100
	case Hit50:
		return 50
	default:
		return 0
	}
}
