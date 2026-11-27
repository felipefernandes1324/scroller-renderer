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
	fps := flag.Float64("fps", 60, "output frames per second")
	motionBlur := flag.Int("motionblur", 1, "temporal samples per output frame")
	speed := flag.Float64("speed", 1, "playback speed (0.5..2)")
	scrollSpeed := flag.Float64("scrollspeed", 24, "osu!mania scroll speed (5..40)")
	width := flag.Int("w", 1280, "video width")
	height := flag.Int("h", 720, "video height")
	flag.Parse()
	if err := run(*osuPath, *osrPath, *skinPath, *output, *fps, *motionBlur, *speed, *scrollSpeed, *width, *height); err != nil {
		log.Fatal(err)
	}
}

func run(osuPath, osrPath, skinPath, output string, fps float64, motionBlur int, speed, scrollSpeed float64, width, height int) error {
	if osuPath == "" {
		return fmt.Errorf("-osu is required")
	}
	if fps <= 0 || fps > 240 || motionBlur < 1 || motionBlur > 32 || speed < 0.5 || speed > 2 ||
		scrollSpeed < 5 || scrollSpeed > 40 || width < 16 || height < 16 || width%2 != 0 || height%2 != 0 {
		return fmt.Errorf("invalid render options: fps must be (0,240], motionblur 1..32, speed 0.5..2, scrollspeed 5..40, and dimensions even and >=16")
	}
	if err := encoder.Check(); err != nil {
		return err
	}
	m, err := beatmap.Parse(osuPath)
	if err != nil {
		return fmt.Errorf("parse beatmap: %w", err)
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
	minimumAudioSeconds := (maxTime + 1500) / 1000
	duration, err := audio.Mix(m.AudioPath(), skinPath, filepath.Dir(osuPath), wavPath, stats.Results, minimumAudioSeconds)
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
	}
	frameBuffer := image.NewRGBA(image.Rect(0, 0, width, height))
	accumulated := make([]uint32, width*height*4)
	averaged := make([]byte, len(accumulated))
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
	}
	if err := enc.Close(); err != nil {
		enc = nil
		return err
	}
	enc = nil
	return nil
}
