"""Orrery's bounded, offline speech adapter. No network or agent tools."""
import base64
import io
import json
import os
from pathlib import Path
import resource
import subprocess
import sys
import wave

resource.setrlimit(resource.RLIMIT_AS, (2 * 1024**3, 2 * 1024**3))
resource.setrlimit(resource.RLIMIT_CPU, (80, 80))
resource.setrlimit(resource.RLIMIT_FSIZE, (32 * 1024**2, 32 * 1024**2))
os.umask(0o077)
asr, tts, scratch = map(Path, sys.argv[1:4])
request = json.loads(sys.stdin.buffer.read(24 * 1024**2))


def convert(args, data, timeout=15):
    result = subprocess.run(
        ["ffmpeg", "-v", "error", "-nostdin", "-threads", "1",
         "-protocol_whitelist", "file,pipe", *args],
        input=data, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        timeout=timeout, check=True,
    )
    return result.stdout


try:
    import numpy as np
    import sherpa_onnx
    if request["operation"] == "transcribe":
        model = asr / "model.int8.onnx"
        if not model.is_file() or not (asr / "tokens.txt").is_file():
            sys.exit(10)
        data = base64.b64decode(request["data"], validate=True)
        path = scratch / "input.audio"
        path.write_bytes(data)
        pcm = convert(["-i", str(path), "-t", "121", "-ac", "1", "-ar", "16000",
                       "-threads", "1", "-f", "f32le", "pipe:1"], None)
        if len(pcm) == 0 or len(pcm) >= 120 * 16000 * 4:
            sys.exit(11)
        recognizer = sherpa_onnx.OfflineRecognizer.from_sense_voice(
            model=str(model), tokens=str(asr / "tokens.txt"), num_threads=2,
            language=request.get("language") or "auto", use_itn=True, provider="cpu",
        )
        stream = recognizer.create_stream()
        stream.accept_waveform(16000, np.frombuffer(pcm, dtype=np.float32))
        recognizer.decode_stream(stream)
        text = stream.result.text.strip()
        if not text:
            sys.exit(12)
        sys.stdout.write(json.dumps({"text": text}, ensure_ascii=False))
    elif request["operation"] == "synthesize":
        model = tts / "model.int8.onnx"
        if not model.is_file():
            model = tts / "model.onnx"
        if not model.is_file():
            sys.exit(10)
        config = sherpa_onnx.OfflineTtsConfig(
            model=sherpa_onnx.OfflineTtsModelConfig(
                vits=sherpa_onnx.OfflineTtsVitsModelConfig(
                    model=str(model), tokens=str(tts / "tokens.txt"),
                    lexicon=str(tts / "lexicon.txt"), dict_dir=str(tts / "dict"),
                ), num_threads=2, provider="cpu",
            ), max_num_sentences=1,
            rule_fsts=",".join(str(tts / name) for name in ("date.fst", "number.fst") if (tts / name).is_file()),
        )
        synthesizer = sherpa_onnx.OfflineTts(config)
        audio = synthesizer.generate(request["text"], sid=0, speed=1.0)
        if len(audio.samples) == 0 or len(audio.samples) > audio.sample_rate * 120:
            sys.exit(11)
        buffer = io.BytesIO()
        with wave.open(buffer, "wb") as wav:
            wav.setnchannels(1)
            wav.setsampwidth(2)
            wav.setframerate(audio.sample_rate)
            wav.writeframes((np.clip(audio.samples, -1, 1) * 32767).astype("<i2").tobytes())
        mp3 = convert(["-i", "pipe:0", "-threads", "1", "-f", "mp3", "-b:a", "64k", "pipe:1"], buffer.getvalue())
        sys.stdout.buffer.write(mp3)
    else:
        sys.exit(11)
except (ImportError, FileNotFoundError):
    sys.exit(10)
except Exception:
    sys.exit(13)
