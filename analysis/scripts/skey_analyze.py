# /// script
# requires-python = ">=3.12,<3.13"
# dependencies = [
#     "skey @ git+https://github.com/deezer/skey@80d9590e150b836a341dfc6dbf05aa20bc81d10b",
# ]
# ///
"""Deezer's S-KEY key detection on one audio file, printed as JSON:

    skey_analyze.py FILE -> {"key": "A minor"}
"""

import contextlib
import json
import logging
import sys

logging.disable(logging.CRITICAL)

import torch  # noqa: E402
from skey.key_detection import (  # noqa: E402
    infer_key,
    load_audio,
    load_checkpoint,
    load_model_components,
)


def main():
    path = sys.argv[1]
    device = torch.device("cpu")
    # nnAudio prints progress to stdout, which is for the JSON alone.
    with contextlib.redirect_stdout(sys.stderr):
        ckpt = load_checkpoint()
        hcqt, chromanet, crop = load_model_components(ckpt, device)
        audio = load_audio(path, ckpt["audio"]["sr"]).to(device)
        key = infer_key(hcqt, chromanet, crop, audio, device)
    if key == "error":
        print("inference failed (audio too short?)", file=sys.stderr)
        sys.exit(1)
    json.dump({"key": key}, sys.stdout)


if __name__ == "__main__":
    main()
