# /// script
# requires-python = ">=3.12,<3.13"
# dependencies = ["essentia==2.1b6.dev1389", "numpy<2"]
# ///
"""Essentia analysis of one audio file, printed as one JSON object.

    essentia_analyze.py bpm FILE  -> {"bpm": 124.0, "confidence": 3.9}
    essentia_analyze.py key FILE  -> {"keys": [{"profile": "edma", "key": "A minor", "strength": 0.7}, ...]}

The key profiles are the ones Essentia tuned for electronic music: edma
(EDM, automatic), bgate (Beatport-derived) and braw.

bpm streams the track from ffmpeg in 30-second windows, so memory stays
flat however long the track is (loading a 60-minute mix whole would take
635 MB before any analysis). The BPM is the median of the windows'
estimates: on real tracks it lands within 0.05 BPM of the whole-track
estimate, and it ignores a window that guesses half or double tempo.
"""

import json
import statistics
import subprocess
import sys

import essentia

essentia.log.infoActive = False
essentia.log.warningActive = False

import essentia.standard as es  # noqa: E402

import numpy as np  # noqa: E402

KEY_PROFILES = ["edma", "bgate", "braw"]

SAMPLE_RATE = 44100
WINDOW_SECONDS = 30
MIN_SECONDS = 5  # a shorter tail is dropped, unless it is the whole track


def windows(path):
    """Yields the track as mono float32 arrays WINDOW_SECONDS long, decoded
    by ffmpeg as they are read."""
    try:
        proc = subprocess.Popen(
            ["ffmpeg", "-nostdin", "-v", "error", "-i", path, "-ac", "1", "-ar", str(SAMPLE_RATE), "-f", "f32le", "-"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
    except FileNotFoundError:
        sys.exit("ffmpeg not found on PATH: install it (brew install ffmpeg)")
    size = SAMPLE_RATE * WINDOW_SECONDS * 4
    first = True
    while True:
        chunk = proc.stdout.read(size)
        if not chunk or (len(chunk) < SAMPLE_RATE * MIN_SECONDS * 4 and not first):
            break
        first = False
        yield np.frombuffer(chunk, dtype=np.float32).copy()
    proc.stdout.close()
    err = proc.stderr.read().decode().strip()
    if proc.wait() != 0:
        sys.exit("ffmpeg: " + (err.splitlines() or ["cannot decode"])[-1])


def bpm(path):
    extract = es.RhythmExtractor2013(method="multifeature")
    bpms, confidences = [], []
    for audio in windows(path):
        b, _, confidence, _, _ = extract(audio)
        if b > 0:
            bpms.append(float(b))
            confidences.append(float(confidence))
    if not bpms:
        sys.exit("no beats found (silent or too short)")
    return {"bpm": statistics.median(bpms), "confidence": statistics.fmean(confidences)}


def key(path):
    audio = es.MonoLoader(filename=path, sampleRate=44100)()
    keys = []
    for profile in KEY_PROFILES:
        k, scale, strength = es.KeyExtractor(profileType=profile)(audio)
        keys.append({"profile": profile, "key": f"{k} {scale}", "strength": float(strength)})
    return {"keys": keys}


def main():
    mode, path = sys.argv[1], sys.argv[2]
    out = {"bpm": bpm, "key": key}[mode](path)
    json.dump(out, sys.stdout)


if __name__ == "__main__":
    main()
