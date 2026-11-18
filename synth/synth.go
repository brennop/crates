// Package synth plays short piano-like notes on the default audio output,
// for the UI's piano panel. Notes are synthesized in process: no files, no
// subprocesses. Each note is rendered once, then cached.
package synth

import (
	"bytes"
	"encoding/binary"
	"math"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

const (
	sampleRate = 44100
	noteLength = 1500 * time.Millisecond
	release    = 60 * time.Millisecond // fade at the end, so a note never clicks off
	attack     = 4 * time.Millisecond
	gain       = 0.22
	maxVoices  = 12 // the oldest note is cut when more ring at once
)

// oto allows one context per process, so every Synth shares it.
var (
	ctxOnce sync.Once
	otoCtx  *oto.Context
	ctxErr  error
)

// Synth plays notes. The zero value is not usable; call New.
type Synth struct {
	mu      sync.Mutex
	started bool
	voices  []*oto.Player
	cache   map[int][]byte // MIDI note to rendered samples
}

func New() *Synth { return &Synth{cache: map[int][]byte{}} }

// Start opens the audio output, the first time, or resumes it after Stop.
// It blocks until the device is ready.
func (s *Synth) Start() error {
	ctxOnce.Do(func() {
		var ready chan struct{}
		otoCtx, ready, ctxErr = oto.NewContext(&oto.NewContextOptions{
			SampleRate:   sampleRate,
			ChannelCount: 1,
			Format:       oto.FormatFloat32LE,
			BufferSize:   20 * time.Millisecond, // keypress to sound latency
		})
		if ctxErr == nil {
			<-ready
		}
	})
	if ctxErr != nil {
		return ctxErr
	}
	if err := otoCtx.Resume(); err != nil {
		return err
	}
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	return nil
}

// Play sounds MIDI note n (60 is middle C). It does nothing before Start.
func (s *Synth) Play(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	live := s.voices[:0]
	for _, p := range s.voices {
		if p.IsPlaying() {
			live = append(live, p)
		}
	}
	s.voices = live
	if len(s.voices) >= maxVoices {
		s.voices[0].Pause()
		s.voices = s.voices[1:]
	}
	pcm, ok := s.cache[n]
	if !ok {
		pcm = render(n)
		s.cache[n] = pcm
	}
	p := otoCtx.NewPlayer(bytes.NewReader(pcm))
	p.SetBufferSize(4096) // small, so the note starts at once
	p.Play()
	s.voices = append(s.voices, p)
}

// Stop cuts every note and suspends the audio output, so it uses no CPU
// while the piano is closed.
func (s *Synth) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.voices {
		p.Pause()
	}
	s.voices = nil
	if s.started {
		s.started = false
		otoCtx.Suspend()
	}
}

// render synthesizes MIDI note n as mono float32 samples: a few harmonics,
// each decaying, the higher ones faster, which reads as a plucked or
// struck string.
func render(n int) []byte {
	f := 440 * math.Pow(2, float64(n-69)/12)
	total := int(noteLength.Seconds() * sampleRate)
	rel := int(release.Seconds() * sampleRate)
	att := attack.Seconds()
	out := make([]byte, total*4)
	for i := range total {
		t := float64(i) / sampleRate
		var v float64
		for h := 1; h <= 6; h++ {
			hf := f * float64(h)
			if hf > sampleRate/2 {
				break
			}
			amp := 1 / math.Pow(float64(h), 1.5)
			v += amp * math.Exp(-t*(2+1.5*float64(h))) * math.Sin(2*math.Pi*hf*t)
		}
		env := min(t/att, 1)
		if left := total - i; left < rel {
			env *= float64(left) / float64(rel)
		}
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(float32(gain*env*v)))
	}
	return out
}
