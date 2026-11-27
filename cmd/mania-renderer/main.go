//go:debug multipartmaxparts=20016

package main

import (
	"flag"
	"fmt"
	"image"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/felipefernandes1324/scroller-renderer/internal/audio"
	"github.com/felipefernandes1324/scroller-renderer/internal/beatmap"
	"github.com/felipefernandes1324/scroller-renderer/internal/encoder"
	"github.com/felipefernandes1324/scroller-renderer/internal/judge"
	"github.com/felipefernandes1324/scroller-renderer/internal/renderer"
	"github.com/felipefernandes1324/scroller-renderer/internal/replay"
	"github.com/felipefernandes1324/scroller-renderer/internal/skin"
	"github.com/felipefernandes1324/scroller-renderer/internal/sv"
)

func main() {
	osuPath := flag.String("osu", "", "beatmap .osu file")
	osrPath := flag.String("osr", "", "replay .osr file (omit for auto-play)")
	skinPath := flag.String("skin", "", "osu! skin folder")
	output := flag.String("out", "mania-render.mp4", "output MP4 file")
	fps := flag.Float64("fps", 60, "output frames per second (max 60)")
	motionBlur := flag.Int("motionblur", 1, "temporal samples per output frame")
	speed := flag.Float64("speed", 1, "playback speed (0.5..2)")
	scrollSpeed := flag.Float64("scrollspeed", 12, "osu!mania scroll speed (5..40)")
	musicVolume := flag.Float64("musicvolume", 100, "music volume (0..100)")
	hitsoundVolume := flag.Float64("hitsoundvolume", 100, "hitsound volume (0..100)")
	gui := flag.Bool("gui", false, "open the local web interface")
	width := flag.Int("w", 1280, "video width")
	height := flag.Int("h", 720, "video height")
	flag.Parse()
	if *gui {
		if err := startGUI(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(*osuPath, *osrPath, *skinPath, *output, *fps, *motionBlur, *speed, *scrollSpeed, *musicVolume, *hitsoundVolume, *width, *height, 0); err != nil {
		log.Fatal(err)
	}
}

func run(osuPath, osrPath, skinPath, output string, fps float64, motionBlur int, speed, scrollSpeed, musicVolume, hitsoundVolume float64, width, height int, starRating float64) error {
	return runWithProgress(osuPath, osrPath, skinPath, output, fps, motionBlur, speed, scrollSpeed, musicVolume, hitsoundVolume, width, height, starRating, nil)
}

func runWithProgress(osuPath, osrPath, skinPath, output string, fps float64, motionBlur int, speed, scrollSpeed, musicVolume, hitsoundVolume float64, width, height int, starRating float64, onProgress func(int)) error {
	if osuPath == "" {
		return fmt.Errorf("-osu is required")
	}
	if fps <= 0 || fps > 60 || motionBlur < 1 || motionBlur > 32 || speed < 0.5 || speed > 2 ||
		scrollSpeed < 5 || scrollSpeed > 40 || musicVolume < 0 || musicVolume > 100 ||
		hitsoundVolume < 0 || hitsoundVolume > 100 || width < 16 || height < 16 || width%2 != 0 || height%2 != 0 {
		return fmt.Errorf("invalid render options: fps must be (0,60], motionblur 1..32, speed 0.5..2, scrollspeed 5..40, volumes 0..100, and dimensions even and >=16")
	}
	if err := encoder.Check(); err != nil {
		return err
	}
	m, err := beatmap.Parse(osuPath)
	if err != nil {
		return fmt.Errorf("parse beatmap: %w", err)
	}
	if starRating > 0 && !math.IsNaN(starRating) && !math.IsInf(starRating, 0) {
		m.StarRating = starRating
	}
	var events []replay.Event
	var replaySummary *replay.ScoreSummary
	if osrPath == "" {
		events = replay.Auto(m.Objects, m.Keys)
	} else {
		parsed, parseErr := replay.ParseReplay(osrPath, m.Keys)
		err = parseErr
		if err != nil {
			return fmt.Errorf("parse replay: %w", err)
		}
		events = parsed.Events
		replaySummary = &parsed.Summary
	}
	stats := judge.Simulate(m.Objects, events, m.OD)
	if replaySummary != nil {
		judge.ApplyReplaySummary(&stats, *replaySummary)
	}
	loadedSkin, err := skin.Load(skinPath, m.Keys)
	if err != nil {
		return fmt.Errorf("load skin: %w", err)
	}
	maxTime := 0.0
	for _, object := range m.Objects {
		if object.End > maxTime {
			maxTime = object.End
		}
	}
	for _, event := range events {
		if event.Time > maxTime {
			maxTime = event.Time
		}
	}
	for _, event := range stats.AccuracyEvents {
		if event.Time > maxTime {
			maxTime = event.Time
		}
	}
	wav, err := os.CreateTemp("", "mania-render-*.wav")
	if err != nil {
		return err
	}
	wavPath := wav.Name()
	if err := wav.Close(); err != nil {
		os.Remove(wavPath)
		return err
	}
	defer os.Remove(wavPath)
	const scoreResultHoldMS = 5000.0
	minimumAudioSeconds := (maxTime + scoreResultHoldMS) / 1000
	duration, err := audio.Mix(m.AudioPath(), skinPath, filepath.Dir(osuPath), wavPath, stats.Results, minimumAudioSeconds, musicVolume, hitsoundVolume)
	if err != nil {
		return fmt.Errorf("mix audio: %w", err)
	}
	if duration <= 0 {
		duration = minimumAudioSeconds
	}
	frameCount := int(math.Ceil(duration / speed * fps))
	if frameCount < 1 {
		frameCount = 1
	}
	enc, err := encoder.Start(width, height, fps, wavPath, output, speed)
	if err != nil {
		return err
	}
	defer func() {
		if enc != nil {
			_ = enc.Close()
		}
	}()
	render := &renderer.Renderer{
		Width: width, Height: height, Map: m, Skin: loadedSkin,
		Timeline: sv.New(m.TimingPoints), Stats: stats, Replay: events, Speed: speed, ScrollSpeed: scrollSpeed, DurationMS: duration * 1000,
		ShowScoreResult: true, ScoreResultStartMS: maxTime,
	}
	frameBuffer := image.NewRGBA(image.Rect(0, 0, width, height))
	accumulated := make([]uint32, width*height*4)
	averaged := make([]byte, len(accumulated))
	lastProgress := 0
	for frame := 0; frame < frameCount; frame++ {
		for i := range accumulated {
			accumulated[i] = 0
		}
		for sample := 0; sample < motionBlur; sample++ {
			timeMS := (float64(frame) + float64(sample)/float64(motionBlur)) * 1000 * speed / fps
			render.FrameInto(timeMS, frameBuffer)
			for i, value := range frameBuffer.Pix {
				accumulated[i] += uint32(value)
			}
		}
		for i, value := range accumulated {
			averaged[i] = byte(value / uint32(motionBlur))
		}
		if err := enc.WriteFrame(averaged); err != nil {
			return fmt.Errorf("write video frame %d: %w", frame, err)
		}
		if onProgress != nil {
			progress := (frame + 1) * 100 / frameCount
			if progress > lastProgress {
				onProgress(progress)
				lastProgress = progress
			}
		}
	}
	if err := enc.Close(); err != nil {
		enc = nil
		return err
	}
	enc = nil
	return nil
}
