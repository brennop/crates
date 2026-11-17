# /// script
# requires-python = ">=3.12,<3.13"
# dependencies = [
#     "skey @ git+https://github.com/deezer/skey@80d9590e150b836a341dfc6dbf05aa20bc81d10b",
# ]
# ///
"""Deezer's S-KEY key detection on one audio file, printed as JSON:

    skey_analyze.py FILE -> {"key": "A minor"}

S-KEY's own loader resamples the whole track with torchaudio, which took
a 7-minute track from 0.4 GB to 2.5 GB, and its detect_key runs the whole
track through the model at once. Here ffmpeg decodes straight to mono at
the model's rate and the track streams through the model in fixed
windows, never whole, so memory stays flat whatever the track's length.
The windows' key probabilities are averaged, weighted by length.
"""

import contextlib
import json
import logging
import subprocess
import sys

logging.disable(logging.CRITICAL)

import torch  # noqa: E402
from skey.key_detection import (  # noqa: E402
    key_map,
    load_checkpoint,
    load_model_components,
)

WINDOW_SECONDS = 20
MIN_SECONDS = 5  # a shorter tail is dropped; the model needs some context
THREADS = 2  # leave the rest of the machine to the user


def windows(path, sr):
    """Yields the track as mono (1, samples) tensors WINDOW_SECONDS long,
    decoded by ffmpeg as they are read and each peak-normalized, as S-KEY's
    loader normalizes a whole track. A tail under MIN_SECONDS is dropped,
    unless it is the whole track."""
    try:
        proc = subprocess.Popen(
            ["ffmpeg", "-nostdin", "-v", "error", "-i", path, "-ac", "1", "-ar", str(sr), "-f", "f32le", "-"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
    except FileNotFoundError:
        sys.exit("ffmpeg not found on PATH: install it (brew install ffmpeg)")
    size = sr * WINDOW_SECONDS * 4
    first = True
    while True:
        chunk = proc.stdout.read(size)
        if not chunk or (len(chunk) < sr * MIN_SECONDS * 4 and not first):
            break
        first = False
        audio = torch.frombuffer(bytearray(chunk), dtype=torch.float32).unsqueeze(0)
        peak = audio.abs().max()
        if peak > 0:
            yield audio / peak
    proc.stdout.close()
    err = proc.stderr.read().decode().strip()
    if proc.wait() != 0:
        sys.exit("ffmpeg: " + (err.splitlines() or ["cannot decode"])[-1])


def main():
    path = sys.argv[1]
    torch.set_num_threads(THREADS)
    device = torch.device("cpu")
    # nnAudio prints progress to stdout, which is for the JSON alone.
    with contextlib.redirect_stdout(sys.stderr):
        ckpt = load_checkpoint()
        sr = ckpt["audio"]["sr"]
        hcqt, chromanet, crop = load_model_components(ckpt, device)
        total, weight = torch.zeros(24), 0
        with torch.no_grad():
            for chunk in windows(path, sr):
                probs = chromanet(crop(hcqt(chunk.unsqueeze(0)), torch.zeros(1)))
                total += probs[0] * chunk.shape[1]
                weight += chunk.shape[1]
    if weight == 0:
        print("no audio to analyze (silent or too short)", file=sys.stderr)
        sys.exit(1)
    json.dump({"key": key_map[int(total.argmax())]}, sys.stdout)


if __name__ == "__main__":
    main()
