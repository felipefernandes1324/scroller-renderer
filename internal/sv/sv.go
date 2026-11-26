package sv

import (
	"math"
	"sort"

	"github.com/felipefernandes1324/scroller-renderer/internal/beatmap"
)

type segment struct {
	start, end, position, velocity float64
}

type Timeline struct {
	segments []segment
}

// New integrates beat-length and inherited SV changes into a signed timeline.
func New(points []beatmap.TimingPoint) *Timeline {
	ordered := append([]beatmap.TimingPoint(nil), points...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Time < ordered[j].Time })
	referenceBeat := 500.0
	for _, p := range ordered {
		if p.Uninherited && p.BeatLength > 0 {
			referenceBeat = p.BeatLength
			break
		}
	}
	velocity := 1.0
	baseVelocity := 1.0
	for _, p := range ordered {
		if p.Time > 0 {
			break
		}
		if p.Uninherited && p.BeatLength > 0 {
			baseVelocity = referenceBeat / p.BeatLength
			velocity = baseVelocity
		} else if !p.Uninherited && p.BeatLength != 0 {
			velocity = baseVelocity * (-100 / p.BeatLength)
		}
	}
	t := &Timeline{}
	position := 0.0
	cursor := 0.0
	for i := 0; i < len(ordered); {
		time := ordered[i].Time
		if time <= 0 {
			i++
			continue
		}
		t.segments = append(t.segments, segment{start: cursor, end: time, position: position, velocity: velocity})
		position += (time - cursor) * velocity
		cursor = time
		for i < len(ordered) && ordered[i].Time == time {
			p := ordered[i]
			if p.Uninherited && p.BeatLength > 0 {
				baseVelocity = referenceBeat / p.BeatLength
				velocity = baseVelocity
			} else if !p.Uninherited {
				if p.BeatLength == 0 {
					velocity = 0
				} else {
					velocity = baseVelocity * (-100 / p.BeatLength)
				}
			}
			i++
		}
	}
	t.segments = append(t.segments, segment{start: cursor, end: math.Inf(1), position: position, velocity: velocity})
	return t
}

func (t *Timeline) Position(ms float64) float64 {
	if len(t.segments) == 0 {
		return ms
	}
	if ms < 0 {
		return ms * t.segments[0].velocity
	}
	i := sort.Search(len(t.segments), func(i int) bool { return t.segments[i].end > ms })
	if i == len(t.segments) {
		i--
	}
	s := t.segments[i]
	return s.position + (ms-s.start)*s.velocity
}
