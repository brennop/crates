package analysis

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// The spectrogram is ffmpeg's showspectrumpic filter, which decodes any
// format and draws the picture itself, written to stdout as a PNG.
//
// showspectrumpic keeps every decoded sample until the end, so memory grows
// with the track. Downmixing to mono halves it: a 9-minute track peaks at
// 0.14 GB (0.23 GB in stereo). Tracks over MaxDuration are not drawn.

// Spectrogram picture size in pixels. The UI scales it to the panel.
const (
	SpectrogramWidth  = 1024
	SpectrogramHeight = 256
)

// ErrNoFFmpeg: ffmpeg is not on PATH, so the spectrogram cannot be drawn.
var ErrNoFFmpeg = errors.New("ffmpeg not found on PATH: install it (brew install ffmpeg)")

// spectrogram draws path's spectrogram as a PNG, without a legend: time
// left to right, frequency linear from 0 Hz at the bottom to half the
// sample rate at the top.
func spectrogram(ctx context.Context, path string) ([]byte, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, ErrNoFFmpeg
	}
	ctx, cancel := context.WithTimeout(ctx, ScriptTimeout)
	defer cancel()
	filter := fmt.Sprintf("aformat=channel_layouts=mono,showspectrumpic=s=%dx%d:legend=0", SpectrogramWidth, SpectrogramHeight)
	stdout, stderr, err := runGroup(ctx, ffmpeg,
		"-nostdin", "-v", "error", "-threads", "1",
		"-i", path, "-lavfi", filter,
		"-f", "image2pipe", "-vcodec", "png", "-")
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrTooMuchMemory) {
			return nil, err
		}
		if msg := lastLine(string(stderr)); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	if len(stdout) == 0 {
		return nil, errors.New("ffmpeg drew no spectrogram")
	}
	return stdout, nil
}
