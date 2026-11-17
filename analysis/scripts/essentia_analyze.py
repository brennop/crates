# /// script
# requires-python = ">=3.12,<3.13"
# dependencies = ["essentia==2.1b6.dev1389", "numpy<2"]
# ///
"""Essentia analysis of one audio file, printed as one JSON object.

    essentia_analyze.py bpm FILE  -> {"bpm": 124.0, "confidence": 3.9}
    essentia_analyze.py key FILE  -> {"keys": [{"profile": "edma", "key": "A minor", "strength": 0.7}, ...]}

The key profiles are the ones Essentia tuned for electronic music: edma
(EDM, automatic), bgate (Beatport-derived) and braw.
"""

import json
import sys

import essentia

essentia.log.infoActive = False
essentia.log.warningActive = False

import essentia.standard as es  # noqa: E402

KEY_PROFILES = ["edma", "bgate", "braw"]


def bpm(path):
    audio = es.MonoLoader(filename=path, sampleRate=44100)()
    bpm, _, confidence, _, _ = es.RhythmExtractor2013(method="multifeature")(audio)
    return {"bpm": float(bpm), "confidence": float(confidence)}


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
