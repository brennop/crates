// Package synth plays piano-like notes on the default audio output, for
// the UI's piano panel. Notes are synthesized in process as they play: no
// files, no subprocesses. A note sounds from NoteOn, decaying slowly like a
// held piano key, until NoteOff damps it.
package synth

import (
	"encoding/binary"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/oto/v3"
)

const (
	sampleRate = 44100
	harmonics  = 6
	attack     = 4 * time.Millisecond
	damp       = 0.07 // seconds for a released note to fall to 1/e
	silence    = 1e-4 // a voice this quiet is finished
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
	voices  []*voice
}

func New() *Synth { return &Synth{} }

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

// NoteOn starts MIDI note n (60 is middle C). A note n already sounding is
// damped first. It does nothing before Start.
func (s *Synth) NoteOn(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	live := s.voices[:0]
	for _, v := range s.voices {
		if !v.player.IsPlaying() {
			continue
		}
		if v.note == n {
			v.release()
		}
		live = append(live, v)
	}
	s.voices = live
	if len(s.voices) >= maxVoices {
		s.voices[0].player.Pause()
		s.voices = s.voices[1:]
	}
	v := newVoice(n)
	v.player = otoCtx.NewPlayer(v)
	v.player.SetBufferSize(4096) // small, so the note starts and stops at once
	v.player.Play()
	s.voices = append(s.voices, v)
}

// NoteOff damps MIDI note n.
func (s *Synth) NoteOff(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.voices {
		if v.note == n {
			v.release()
		}
	}
}

// Stop cuts every note and suspends the audio output, so it uses no CPU
// while the piano is closed.
func (s *Synth) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.voices {
		v.player.Pause()
	}
	s.voices = nil
	if s.started {
		s.started = false
		otoCtx.Suspend()
	}
}

// voice is one sounding note, read by its oto player as mono float32
// samples: a few harmonics, each decaying, the higher ones faster, which
// reads as a struck string. Once released, all of it falls off quickly.
type voice struct {
	note     int
	player   *oto.Player
	released atomic.Bool

	i      int                // samples written
	phase  [harmonics]float64 // radians
	step   [harmonics]float64 // radians per sample
	amp    [harmonics]float64 // current amplitude
	decay  [harmonics]float64 // per-sample factor while held
	nh     int                // harmonics under Nyquist
	damper float64            // 1 until released, then falling
}

func newVoice(n int) *voice {
	f := 440 * math.Pow(2, float64(n-69)/12)
	v := &voice{note: n, damper: 1}
	for h := range harmonics {
		hf := f * float64(h+1)
		if hf >= sampleRate/2 {
			break
		}
		v.nh++
		v.step[h] = 2 * math.Pi * hf / sampleRate
		v.amp[h] = 1 / math.Pow(float64(h+1), 1.5)
		// Held, the fundamental takes about 3 s to fall to 1/e, the
		// highest harmonic about half a second.
		v.decay[h] = math.Exp(-(0.35 + 0.3*float64(h)) / sampleRate)
	}
	return v
}

func (v *voice) release() { v.released.Store(true) }

var dampStep = math.Exp(-1 / (damp * sampleRate))

func (v *voice) Read(buf []byte) (int, error) {
	att := attack.Seconds() * sampleRate
	n := 0
	for ; n+4 <= len(buf); n += 4 {
		if v.released.Load() {
			v.damper *= dampStep
		}
		var s, level float64
		for h := range v.nh {
			s += v.amp[h] * math.Sin(v.phase[h])
			level += v.amp[h]
			v.phase[h] = math.Mod(v.phase[h]+v.step[h], 2*math.Pi)
			v.amp[h] *= v.decay[h]
		}
		if level*v.damper < silence {
			if n == 0 {
				return 0, io.EOF
			}
			break
		}
		env := min(float64(v.i)/att, 1) * v.damper
		binary.LittleEndian.PutUint32(buf[n:], math.Float32bits(float32(gain*env*s)))
		v.i++
	}
	return n, nil
}
