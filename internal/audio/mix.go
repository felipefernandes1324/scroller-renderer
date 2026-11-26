package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/felipefernandes1324/scroller-renderer/internal/judge"
)

const (
	sampleRate = 44100
	channels   = 2
)

func Mix(audioPath, skinDir, mapDir, outPath string, results []judge.Result, minimumDuration float64) (float64, error) {
	var pcm []int16
	if audioPath != "" {
		if _, err := os.Stat(audioPath); err != nil {
			return 0, fmt.Errorf("beatmap audio: %w", err)
		}
		decoded, err := decode(audioPath)
		if err != nil {
			return 0, fmt.Errorf("decode beatmap audio: %w", err)
		}
		pcm = decoded
	}
	minimumSamples := int(math.Ceil(minimumDuration*sampleRate)) * channels
	if minimumSamples > len(pcm) {
		pcm = append(pcm, make([]int16, minimumSamples-len(pcm))...)
	}
	samples := make(map[string][]int16)
	for _, result := range results {
		if result.Grade == judge.Miss {
			continue
		}
		names := []string{"normal-hitnormal.wav"}
		if result.Sound&2 != 0 {
			names = append(names, "normal-hitwhistle.wav")
		}
		if result.Sound&4 != 0 {
			names = append(names, "normal-hitfinish.wav")
		}
		if result.Sound&8 != 0 {
			names = append(names, "normal-hitclap.wav")
		}
		if result.Sample != "" {
			names = []string{result.Sample}
		}
		start := int(result.Time * sampleRate * channels / 1000)
		primaryPlayed := false
		for i, name := range names {
			path := findSample(name, skinDir, mapDir)
			if path == "" && result.Sample != "" {
				candidate := filepath.Join(mapDir, filepath.FromSlash(name))
				if _, err := os.Stat(candidate); err == nil {
					path = candidate
				} else {
					return 0, fmt.Errorf("custom hitsound %q not found in map folder", name)
				}
			}
			if path == "" {
				if result.Sample == "" && i == 0 {
					sample, ok := samples["<fallback>"]
					if !ok {
						sample = fallbackHit()
						samples["<fallback>"] = sample
					}
					mix(pcm, sample, start)
					primaryPlayed = true
				}
				continue
			}
			sample, ok := samples[path]
			if !ok {
				decoded, err := decode(path)
				if err != nil {
					return 0, fmt.Errorf("decode hitsound %s: %w", path, err)
				}
				sample = decoded
				samples[path] = sample
			}
			mix(pcm, sample, start)
			if i == 0 {
				primaryPlayed = true
			}
		}
		if !primaryPlayed && result.Sample == "" {
			fallback := samples["<fallback>"]
			if len(fallback) == 0 {
				fallback = fallbackHit()
				samples["<fallback>"] = fallback
			}
			mix(pcm, fallback, start)
		}
	}
	if err := writeWAV(outPath, pcm); err != nil {
		return 0, err
	}
	return float64(len(pcm)) / (sampleRate * channels), nil
}

func decode(path string) ([]int16, error) {
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "s16le", "-acodec", "pcm_s16le", "-ac", "2", "-ar", fmt.Sprint(sampleRate), "pipe:1")
	data, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, err
	}
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("decoder returned an incomplete PCM sample")
	}
	pcm := make([]int16, len(data)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return pcm, nil
}

func findSample(name, skinDir, mapDir string) string {
	for _, root := range []string{skinDir, mapDir} {
		if root == "" {
			continue
		}
		baseName := strings.TrimPrefix(name, "normal-")
		candidates := []string{
			filepath.Join(root, name), filepath.Join(root, baseName),
			filepath.Join(root, "hitsounds", name), filepath.Join(root, "hitsounds", baseName),
		}
		if name == "normal-hitnormal.wav" {
			candidates = append(candidates,
				filepath.Join(root, "soft-hitnormal.wav"), filepath.Join(root, "drum-hitnormal.wav"),
				filepath.Join(root, "hitsounds", "soft-hitnormal.wav"), filepath.Join(root, "hitsounds", "drum-hitnormal.wav"))
		}
		for _, path := range candidates {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	return ""
}

func fallbackHit() []int16 {
	pcm := make([]int16, sampleRate/16*channels)
	for frame := 0; frame < len(pcm)/channels; frame++ {
		envelope := math.Exp(-float64(frame) / 95)
		value := int16(math.Sin(2*math.Pi*1150*float64(frame)/sampleRate) * envelope * 5000)
		pcm[frame*channels], pcm[frame*channels+1] = value, value
	}
	return pcm
}

func mix(dst, src []int16, start int) {
	if start < 0 {
		src = src[-start:]
		start = 0
	}
	if start >= len(dst) {
		return
	}
	end := start + len(src)
	if end > len(dst) {
		end = len(dst)
	}
	for i := start; i < end; i++ {
		value := int32(dst[i]) + int32(src[i-start])
		if value > 32767 {
			value = 32767
		} else if value < -32768 {
			value = -32768
		}
		dst[i] = int16(value)
	}
}

func writeWAV(path string, pcm []int16) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	dataSize := uint32(len(pcm) * 2)
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 36+dataSize)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], channels)
	binary.LittleEndian.PutUint32(header[24:28], sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], sampleRate*channels*2)
	binary.LittleEndian.PutUint16(header[32:34], channels*2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)
	if _, err := file.Write(header); err != nil {
		return err
	}
	buffer := make([]byte, 64*1024)
	for len(pcm) > 0 {
		count := len(buffer) / 2
		if count > len(pcm) {
			count = len(pcm)
		}
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint16(buffer[i*2:], uint16(pcm[i]))
		}
		if _, err := file.Write(buffer[:count*2]); err != nil {
			return err
		}
		pcm = pcm[count:]
	}
	return nil
}
