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
the model's rate (about 35 MB for 7 minutes), the track goes through the
model in fixed windows, and the windows' key probabilities are averaged,
weighted by length, so memory stays flat whatever the track's length.
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


def load_audio(path, sr):
    """Decodes path to mono float32 at sr with ffmpeg, peak-normalized
    as S-KEY's loader does. Returns a (1, samples) tensor."""
    try:
        out = subprocess.run(
            ["ffmpeg", "-nostdin", "-v", "error", "-i", path, "-ac", "1", "-ar", str(sr), "-f", "f32le", "-"],
            capture_output=True,
            check=True,
        ).stdout
    except FileNotFoundError:
        sys.exit("ffmpeg not found on PATH: install it (brew install ffmpeg)")
    except subprocess.CalledProcessError as e:
        sys.exit("ffmpeg: " + (e.stderr.decode().strip().splitlines() or ["cannot decode"])[-1])
    audio = torch.frombuffer(bytearray(out), dtype=torch.float32).unsqueeze(0)
    peak = audio.abs().max() if audio.numel() else 0
    return audio / peak if peak > 0 else audio


def main():
    path = sys.argv[1]
    torch.set_num_threads(THREADS)
    device = torch.device("cpu")
    # nnAudio prints progress to stdout, which is for the JSON alone.
    with contextlib.redirect_stdout(sys.stderr):
        ckpt = load_checkpoint()
        sr = ckpt["audio"]["sr"]
        hcqt, chromanet, crop = load_model_components(ckpt, device)
        audio = load_audio(path, sr)  # (1, samples), mono
        win, least = WINDOW_SECONDS * sr, MIN_SECONDS * sr
        total, weight = torch.zeros(24), 0
        with torch.no_grad():
            for start in range(0, audio.shape[1], win):
                chunk = audio[:, start : start + win]
                n = chunk.shape[1]
                if n < least and weight > 0:
                    break
                if torch.max(torch.abs(chunk)) == 0:
                    continue
                probs = chromanet(crop(hcqt(chunk.unsqueeze(0)), torch.zeros(1)))
                total += probs[0] * n
                weight += n
    if weight == 0:
        print("no audio to analyze (silent or too short)", file=sys.stderr)
        sys.exit(1)
    json.dump({"key": key_map[int(total.argmax())]}, sys.stdout)


if __name__ == "__main__":
    main()
