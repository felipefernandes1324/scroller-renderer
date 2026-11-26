package encoder

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

type Encoder struct {
	cmd   *exec.Cmd
	input io.WriteCloser
	log   *strings.Builder
}

func Start(width, height int, fps float64, audioPath, output string, speed float64) (*Encoder, error) {
	args := []string{
		"-y", "-v", "error",
		"-f", "rawvideo", "-pixel_format", "rgba",
		"-video_size", fmt.Sprintf("%dx%d", width, height),
		"-framerate", strconv.FormatFloat(fps, 'f', -1, 64),
		"-i", "pipe:0",
		"-i", audioPath,
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", "libx264", "-preset", "medium", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "192k", "-shortest", "-movflags", "+faststart",
	}
	if speed != 1 {
		args = append(args, "-filter:a", "atempo="+strconv.FormatFloat(speed, 'f', 4, 64))
	}
	args = append(args, output)
	cmd := exec.Command("ffmpeg", args...)
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	log := &strings.Builder{}
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		input.Close()
		return nil, fmt.Errorf("start ffmpeg (install it and ensure it is on PATH): %w", err)
	}
	return &Encoder{cmd: cmd, input: input, log: log}, nil
}

func (e *Encoder) WriteFrame(rgba []byte) error {
	_, err := e.input.Write(rgba)
	return err
}

func (e *Encoder) Close() error {
	if err := e.input.Close(); err != nil {
		_ = e.cmd.Wait()
		return err
	}
	if err := e.cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg encoding failed: %w: %s", err, strings.TrimSpace(e.log.String()))
	}
	return nil
}

func Check() error {
	cmd := exec.Command("ffmpeg", "-version")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg is required and must be available on PATH: %w", err)
	}
	return nil
}
