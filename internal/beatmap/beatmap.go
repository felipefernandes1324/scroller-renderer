package beatmap

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type TimingPoint struct {
	Time       float64
	BeatLength float64
	Uninherited bool
}

type HitObject struct {
	Time  float64
	End   float64
	Lane  int
	Long  bool
	Sound int
	Sample string
}

type Map struct {
	Path          string
	AudioFilename string
	Mode          int
	Keys          int
	OD            float64
	Objects       []HitObject
	TimingPoints  []TimingPoint
}

func Parse(path string) (*Map, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	m := &Map{Path: path, Keys: 4, OD: 5}
	section := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "osu file format") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			continue
		}
		switch section {
		case "General":
			if key, value, ok := strings.Cut(line, ":"); ok {
				switch strings.TrimSpace(key) {
				case "AudioFilename":
					m.AudioFilename = strings.TrimSpace(value)
				case "Mode":
					mode, err := strconv.Atoi(strings.TrimSpace(value))
					if err != nil {
						return nil, fmt.Errorf("line %d: invalid game mode: %w", lineNo, err)
					}
					m.Mode = mode
				}
			}
		case "Difficulty":
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid %s value: %w", lineNo, key, err)
			}
			switch strings.TrimSpace(key) {
			case "CircleSize":
				m.Keys = int(n + 0.5)
			case "OverallDifficulty":
				m.OD = n
			}
		case "TimingPoints":
			fields := strings.Split(line, ",")
			if len(fields) < 2 {
				return nil, fmt.Errorf("line %d: malformed timing point", lineNo)
			}
			t, e1 := strconv.ParseFloat(fields[0], 64)
			beat, e2 := strconv.ParseFloat(fields[1], 64)
			uninherited := true
			if len(fields) > 6 {
				uninherited = strings.TrimSpace(fields[6]) == "1"
			}
			if e1 != nil || e2 != nil {
				return nil, fmt.Errorf("line %d: invalid timing point values", lineNo)
			}
			m.TimingPoints = append(m.TimingPoints, TimingPoint{Time: t, BeatLength: beat, Uninherited: uninherited})
		case "HitObjects":
			fields := strings.Split(line, ",")
			if len(fields) < 5 {
				return nil, fmt.Errorf("line %d: malformed hit object", lineNo)
			}
			x, e1 := strconv.Atoi(fields[0])
			t, e2 := strconv.ParseFloat(fields[2], 64)
			kind, e3 := strconv.Atoi(fields[3])
			sound, e4 := strconv.Atoi(fields[4])
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
				return nil, fmt.Errorf("line %d: invalid hit object values", lineNo)
			}
			if kind&1 == 0 && kind&128 == 0 {
				continue
			}
			lane := x * m.Keys / 512
			if lane < 0 {
				lane = 0
			}
			if lane >= m.Keys {
				lane = m.Keys - 1
			}
			object := HitObject{Time: t, End: t, Lane: lane, Long: kind&128 != 0, Sound: sound}
			if len(fields) > 6 {
				parts := strings.Split(fields[6], ":")
				object.Sample = strings.TrimSpace(parts[len(parts)-1])
			}
			if object.Long {
				if len(fields) < 6 {
					return nil, fmt.Errorf("line %d: long note has no end time", lineNo)
				}
				end := strings.SplitN(fields[5], ":", 2)[0]
				object.End, err = strconv.ParseFloat(end, 64)
				if err != nil || object.End < object.Time {
					return nil, fmt.Errorf("line %d: invalid long-note end time", lineNo)
				}
			}
			m.Objects = append(m.Objects, object)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if m.Mode != 3 {
		return nil, fmt.Errorf("beatmap mode is %d; expected mania (3)", m.Mode)
	}
	if m.Keys < 1 || m.Keys > 18 {
		return nil, fmt.Errorf("unsupported mania key count %d (expected 1..18)", m.Keys)
	}
	sort.SliceStable(m.TimingPoints, func(i, j int) bool { return m.TimingPoints[i].Time < m.TimingPoints[j].Time })
	sort.SliceStable(m.Objects, func(i, j int) bool { return m.Objects[i].Time < m.Objects[j].Time })
	return m, nil
}

func (m *Map) AudioPath() string {
	if m.AudioFilename == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(m.Path), filepath.FromSlash(m.AudioFilename))
}
