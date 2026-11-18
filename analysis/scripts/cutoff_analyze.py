# /// script
# requires-python = ">=3.12,<3.13"
# dependencies = ["numpy==2.5.4", "scikit-learn==1.9.1"]
# ///
"""Does an MP3's high-frequency spectrum fit the bitrate in its header?
Printed as one JSON object:

    cutoff_analyze.py FILE -> {"status": "FAKE", "claimed_kbps": 320, "estimated_kbps": 128,
                               "cutoff_hz": 16700, "expected_hz": 20500, "score": 0.97}

status is OK, SUSPECT, FAKE or UNKNOWN (too little treble to judge by).
cutoff_hz is where the content really stops, for flagged files only;
expected_hz is the lowpass a genuine encode with this header has.

This is the detection path of ~/projects/cutoff (detect.analyze), cut
down to one file: decode, per-frame levels in 100 Hz bands from 8 to
22 kHz, the per-frame level step at every band edge over the frames with
the most treble, and a gradient-boosted classifier trained on genuine
and transcoded LAME encodes. Only the feature blocks the classifier reads
are computed. The training table, cutoff_training.npz, sits beside this
script; the fitted model is cached next to it per scikit-learn version.
To change the method, change it in cutoff, run `cutoff calibrate`, and
copy src/cutoff/training.npz here.
"""

import hashlib
import json
import os
import pickle
import struct
import subprocess
import sys
from pathlib import Path

THREADS = "2"  # leave the rest of the machine to the user
for v in ("OMP_NUM_THREADS", "OPENBLAS_NUM_THREADS", "MKL_NUM_THREADS", "VECLIB_MAXIMUM_THREADS"):
    os.environ.setdefault(v, THREADS)

import numpy as np  # noqa: E402

HERE = Path(__file__).resolve().parent
TRAINING = HERE / "cutoff_training.npz"

# --- verdict thresholds and LAME lowpasses (cutoff/model.py) ---------------------

FAKE_AT = 0.5
SUSPECT_AT = 0.02
QUIET_TREBLE_DB = -80.0
CBR_LOWPASS_KHZ = {64: 11.0, 80: 13.5, 96: 15.1, 112: 15.6, 128: 17.0, 160: 17.5, 192: 18.6,
                   224: 19.4, 256: 19.7, 320: 20.5}
CBR_CLIFF_KHZ = {64: 11.0, 80: 13.7, 96: 15.1, 112: 15.4, 128: 16.7, 160: 17.4, 192: 18.8,
                 224: 19.5}
EDGE0_KHZ, EDGE_STEP_KHZ = 10.0, 0.1

# --- band levels (cutoff/spectrum.py) --------------------------------------------

NFFT = 4096
HOP = NFFT // 2
SILENCE_DBFS = -70.0
RATE = 44100
CHUNK_FRAMES = 1 << 18
BAND_EDGES = np.arange(8000.0, 22000.0 + 1, 100.0)
BAND_IDX = np.searchsorted(np.fft.rfftfreq(NFFT, 1 / RATE), BAND_EDGES)


def _blackman_harris(n):
    a = (0.35875, 0.48829, 0.14128, 0.01168)
    k = np.arange(n) / n * 2 * np.pi
    return (a[0] - a[1] * np.cos(k) + a[2] * np.cos(2 * k) - a[3] * np.cos(3 * k)).astype(np.float32)


WIN = _blackman_harris(NFFT)
WIN_POWER = float(np.sum(WIN.astype(np.float64) ** 2))


def probe(path):
    try:
        out = subprocess.run(
            ["ffprobe", "-v", "error", "-select_streams", "a:0",
             "-show_entries", "stream=codec_name,bit_rate:format=bit_rate", "-of", "json", path],
            capture_output=True, text=True)
    except FileNotFoundError:
        sys.exit("ffprobe not found on PATH: install ffmpeg (brew install ffmpeg)")
    if out.returncode != 0:
        sys.exit("ffprobe: " + (out.stderr.strip().splitlines() or ["cannot read"])[-1])
    data = json.loads(out.stdout)
    if not data.get("streams"):
        sys.exit("no audio stream")
    s, f = data["streams"][0], data.get("format", {})
    br = s.get("bit_rate") or f.get("bit_rate")
    return s.get("codec_name", "?"), int(br) if br and str(br).isdigit() else 0


def band_frames(path):
    """Level (dB) of every non-silent frame in 100 Hz bands from 8 to 22 kHz,
    (frames, 140) float32, decoded by ffmpeg as it is read."""
    proc = subprocess.Popen(
        ["ffmpeg", "-v", "error", "-nostdin", "-i", path, "-map", "0:a:0",
         "-f", "f32le", "-acodec", "pcm_f32le", "-ar", str(RATE), "-ac", "2", "-"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    ch = 2
    width = (BAND_IDX[1:] - BAND_IDX[:-1]).astype(np.float64)
    out = []
    tail = np.zeros((0, ch), dtype=np.float32)
    buf = b""
    while True:
        data = proc.stdout.read(CHUNK_FRAMES * 4 * ch)
        if not data:
            break
        buf += data
        usable = len(buf) - len(buf) % (4 * ch)
        chunk = np.frombuffer(buf[:usable], dtype="<f4").reshape(-1, ch)
        buf = buf[usable:]
        x = np.concatenate([tail, chunk]) if len(tail) else chunk
        n = (len(x) - NFFT) // HOP + 1
        if n <= 0:
            tail = x
            continue
        idx = np.arange(NFFT)[None, :] + HOP * np.arange(n)[:, None]
        frames = x[idx]
        rms = 10 * np.log10(np.mean(frames.astype(np.float64) ** 2, axis=(1, 2)) + 1e-30)
        keep = rms > SILENCE_DBFS
        tail = x[n * HOP:]
        if not keep.any():
            continue
        p = np.zeros((int(keep.sum()), NFFT // 2 + 1), dtype=np.float64)
        for c in range(ch):
            s = np.fft.rfft(frames[keep, :, c] * WIN, axis=1)
            p += (s.real.astype(np.float64) ** 2 + s.imag.astype(np.float64) ** 2) / (WIN_POWER * ch)
        cs = np.concatenate([np.zeros((len(p), 1)), np.cumsum(p, axis=1)], axis=1)
        out.append((10 * np.log10((cs[:, BAND_IDX[1:]] - cs[:, BAND_IDX[:-1]]) / width + 1e-20)).astype(np.float32))
    proc.stdout.close()
    err = proc.stderr.read().decode(errors="replace").strip()
    if proc.wait() != 0:
        sys.exit("ffmpeg: " + (err.splitlines() or ["cannot decode"])[-1])
    if not out:
        sys.exit("no non-silent audio")
    return np.concatenate(out)


# --- features (cutoff/features.py, only the blocks the model reads) --------------

LOUD_FRACTION = 0.3
LOUD_QUANTILES = (10, 25, 50)
REF = slice(0, 20)  # 8-10 kHz
FROM = 20           # features start at 10 kHz


EDGES_AT_ONCE = 16  # band edges whose per-frame steps are held at once


def feature_blocks(b):
    """As cutoff's, but the per-frame steps are computed a few band edges at
    a time: whole, they took a 58-minute mix to 820 MB."""
    p = b.astype(np.float64)  # 10 ** (b / 10), in place
    p /= 10
    np.power(10.0, p, out=p)
    mean = 10 * np.log10(p.mean(axis=0) + 1e-20)
    ref = mean[REF].mean()
    # cutoff's quantiles, all five: asked alone, the median differs in the last bits
    median = np.percentile(b[:, 60:80], (10, 25, 50, 75, 90), axis=0)[2]
    cs = np.zeros((len(p), p.shape[1] + 1))
    np.cumsum(p, axis=1, out=cs[:, 1:])
    del p
    n_loud = max(8, int(round(len(cs) * LOUD_FRACTION)))
    edges = np.arange(FROM, b.shape[1] - 3)
    loud = np.empty((len(LOUD_QUANTILES), len(edges)))
    for at in range(0, len(edges), EDGES_AT_ONCE):
        k = edges[at:at + EDGES_AT_ONCE]
        lo = 10 * np.log10((cs[:, k - 1] - cs[:, k - 4]) / 3 + 1e-20)
        step = lo - 10 * np.log10((cs[:, k + 4] - cs[:, k + 1]) / 3 + 1e-20)
        order = np.argpartition(-lo, min(n_loud, len(lo) - 1), axis=0)[:n_loud]
        loud[:, at:at + len(k)] = np.percentile(np.take_along_axis(step, order, axis=0), LOUD_QUANTILES, axis=0)
    del cs
    return {
        "loud": loud.ravel().astype(np.float32),
        "mean": (mean[FROM:] - ref).astype(np.float32),
        "abs": np.array([ref, mean[60:80].mean(), median.mean()], dtype=np.float32),
    }


# --- model (cutoff/model.py) -----------------------------------------------------

def header_lowpass_khz(claimed_kbps, lame_lowpass_hz):
    if lame_lowpass_hz:
        return lame_lowpass_hz / 1000
    k = np.array(sorted(CBR_LOWPASS_KHZ), float)
    return float(np.interp(claimed_kbps, k, [CBR_LOWPASS_KHZ[int(b)] for b in k]))


def treble_db(blocks, hdr):
    absmean = blocks["mean"] + blocks["abs"][0]
    a = int((min(hdr, 16.0) - 10.0) * 10)
    return float(absmean[max(a - 20, 0):max(a, 1)].mean())


def source_cliff_khz(loud, hdr):
    p25 = loud.reshape(3, -1)[1]
    f = EDGE0_KHZ + EDGE_STEP_KHZ * np.arange(len(p25))
    m = (f >= 10.5) & (f <= min(hdr, 20.5) - 0.6)
    if not m.any():
        return None
    return float(f[m][np.argmax(p25[m])])


def kbps_for_cliff(khz):
    return min(CBR_CLIFF_KHZ, key=lambda b: abs(CBR_CLIFF_KHZ[b] - khz))


def classifier():
    """The fitted model, from the cache beside this script or fitted from
    the training table (a few seconds) and cached."""
    import sklearn
    from sklearn.ensemble import HistGradientBoostingClassifier

    data = TRAINING.read_bytes()
    tag = hashlib.sha256(data).hexdigest()[:16]
    pkl = HERE / f"cutoff-model-{tag}-sklearn{sklearn.__version__}.pkl"
    if pkl.exists():
        try:
            return pickle.loads(pkl.read_bytes())
        except Exception:  # noqa: BLE001 - a damaged cache is refitted
            pass
    with np.load(TRAINING) as z:
        x, fake = z["x"].astype(np.float32), z["fake"]
    clf = HistGradientBoostingClassifier(max_iter=400, learning_rate=0.06, max_leaf_nodes=15,
                                         min_samples_leaf=40, l2_regularization=1.0, random_state=0)
    clf.fit(x, fake)
    tmp = pkl.with_suffix(f".{os.getpid()}.tmp")
    try:
        tmp.write_bytes(pickle.dumps(clf))
        tmp.replace(pkl)
    except OSError:
        pass
    return clf


# --- MP3 Xing / LAME tag (cutoff/audio.py) ---------------------------------------

def lame_lowpass(path):
    """The lowpass LAME wrote in its tag, in Hz, or None."""
    try:
        with open(path, "rb") as fh:
            head = fh.read(10)
            start = 0
            if head[:3] == b"ID3" and len(head) >= 10:
                start = 10 + ((head[6] << 21) | (head[7] << 14) | (head[8] << 7) | head[9])
                start += 10 if head[5] & 0x10 else 0
            fh.seek(start)
            data = fh.read(64 * 1024)
    except OSError:
        return None
    i = 0
    while i < len(data) - 4:
        if data[i] == 0xFF and (data[i + 1] & 0xE0) == 0xE0:
            b1, b2, b3 = data[i + 1], data[i + 2], data[i + 3]
            version, layer = (b1 >> 3) & 3, (b1 >> 1) & 3
            if layer == 1 and version != 1 and (b2 >> 4) not in (0, 15) and ((b2 >> 2) & 3) != 3:
                break
        i += 1
    else:
        return None
    mono = ((b3 >> 6) & 3) == 3
    side = (17 if mono else 32) if version == 3 else (9 if mono else 17)
    off = i + 4 + side
    if data[off:off + 4] not in (b"Xing", b"Info"):
        return None
    flags = struct.unpack(">I", data[off + 4:off + 8])[0]
    p = off + 8 + (4 if flags & 1 else 0) + (4 if flags & 2 else 0) + (100 if flags & 4 else 0)
    p += 4 if flags & 8 else 0
    # only LAME itself fills this byte; ffmpeg's version string runs over it
    if data[p:p + 4] != b"LAME" or len(data) <= p + 10:
        return None
    lp = data[p + 10]
    return lp * 100 if lp >= 50 else None


# --- verdict (cutoff/detect.py) --------------------------------------------------

def analyze(path):
    codec, bitrate = probe(path)
    claimed = round(bitrate / 1000)
    if not claimed:
        sys.exit("no bitrate in the header")
    frames = band_frames(path)
    blocks = feature_blocks(frames)
    del frames
    hdr = header_lowpass_khz(claimed, lame_lowpass(path) if codec == "mp3" else None)
    x = np.hstack([blocks["loud"], blocks["abs"], [hdr]]).astype(np.float32)[None, :]
    score = float(classifier().predict_proba(x)[0, 1])
    out = {"status": "OK", "claimed_kbps": claimed, "estimated_kbps": 0, "cutoff_hz": 0,
           "expected_hz": round(hdr * 1000), "score": round(score, 4), "codec": codec}
    if score >= SUSPECT_AT:
        out["status"] = "FAKE" if score >= FAKE_AT else "SUSPECT"
        cliff = source_cliff_khz(blocks["loud"], hdr)
        if cliff is not None:
            out["cutoff_hz"] = round(cliff * 1000)
            est = kbps_for_cliff(cliff)
            if est < claimed:
                out["estimated_kbps"] = est
    elif treble_db(blocks, hdr) < QUIET_TREBLE_DB:
        out["status"] = "UNKNOWN"
    return out


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: cutoff_analyze.py FILE")
    print(json.dumps(analyze(sys.argv[1])))
