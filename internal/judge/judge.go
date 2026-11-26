package judge

import (
	"math"
	"sort"

	"github.com/felipefernandes1324/scroller-renderer/internal/beatmap"
	"github.com/felipefernandes1324/scroller-renderer/internal/replay"
)

type Grade string

const (
	MAX  Grade = "mania-hit300g"
	Hit300 Grade = "mania-hit300"
	Hit200 Grade = "mania-hit200"
	Hit100 Grade = "mania-hit100"
	Hit50  Grade = "mania-hit50"
	Miss   Grade = "mania-hit0"
)

type Result struct {
	Time  float64
	Lane  int
	Grade Grade
	Long  bool
	Sound int
	Sample string
	ComboAfter int
	AccuracyAfter float64
}

type Stats struct {
	Combo     int
	MaxCombo  int
	Accuracy  float64
	Results   []Result
}

type keyEvent struct {
	time  float64
	down  bool
	used  bool
}

func Simulate(objects []beatmap.HitObject, replayEvents []replay.Event, od float64) Stats {
	w320 := math.Max(8, 16.5-0.5*od)
	w300 := math.Max(w320, 64-3*od)
	w200 := math.Max(w300, 97-3*od)
	w100 := math.Max(w200, 127-3*od)
	w50 := math.Max(w100, 151-3*od)
	missWindow := math.Max(w50, 188-3*od)

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
		if best >= 0 {
			keys[best].used = true
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
						break
					}
				}
				if !released {
					grade = Miss
				}
			}
		}
		value := gradeValue(grade)
		total += value
		if grade == Miss {
			stats.Combo = 0
		} else {
			stats.Combo++
			if stats.Combo > stats.MaxCombo {
				stats.MaxCombo = stats.Combo
			}
		}
		stats.Results = append(stats.Results, Result{
			Time: object.Time, Lane: object.Lane, Grade: grade, Long: object.Long,
			Sound: object.Sound, Sample: object.Sample, ComboAfter: stats.Combo,
			AccuracyAfter: total / float64(objectIndex+1) / 320 * 100,
		})
	}
	if maxPoints > 0 {
		stats.Accuracy = total / maxPoints * 100
	}
	return stats
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
