package beatmap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseManiaKeysAndLongNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.osu")
	data := `osu file format v14
[General]
AudioFilename: song.ogg
Mode: 3
[Difficulty]
CircleSize: 7
OverallDifficulty: 8
[TimingPoints]
0,500,4,2,1,100,1,0
[HitObjects]
511,192,1000,1,0,0:0:0:0:
256,192,1500,128,0,2200:0:0:0:0:
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Keys != 7 || m.OD != 8 || len(m.Objects) != 2 || !m.Objects[1].Long || m.Objects[1].End != 2200 {
		t.Fatalf("unexpected parsed beatmap: %+v", m)
	}
	if got := filepath.Base(m.AudioPath()); got != "song.ogg" {
		t.Fatalf("AudioPath basename = %q", got)
	}
}
