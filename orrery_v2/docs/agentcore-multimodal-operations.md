# Managed conversation multimodal input

Managed AgentCore receives authorized image bytes through Eino and the configured
Chat Completions or Responses provider. Text context and encoded image transport
have independent limits: 16 MiB of images, at most 32 images, plus the existing
text and model context budgets. Image payloads survive checkpoint/resume. A first
send cannot substitute different images for the authorized runtime request.

Managed discussion attachments are prepared before the model call. UTF-8 text,
bounded DOCX ZIPs and text PDFs become labeled source data. PDF parsing uses the
operator's installed `python3` and `pypdf`, with no application credentials in its
environment. Password-protected and scanned PDFs are not OCR support. Each PDF
is limited to its first 50 pages and 128 KiB of extracted text.

Videos require `ffprobe` and `ffmpeg`: at most 120 seconds, 3–8 chronological JPEG
samples, 960×540 maximum, a 45 second preparation deadline and a private temporary
directory. Motion between samples and the video's audio track are not analyzed.
PI's existing image route remains unchanged; this preprocessor applies to managed
conversations and never writes attachment contents into a knowledge base.

## Optional Linux offline speech

`ORRERY_AUDIO_PROTOCOL=local-sherpa` selects an explicitly configured backend:

```text
ORRERY_AUDIO_LOCAL_PYTHON=/usr/bin/python3
ORRERY_AUDIO_LOCAL_ASR_DIR=/absolute/path/to/sensevoice-int8
ORRERY_AUDIO_LOCAL_TTS_DIR=/absolute/path/to/melotts-zh-en
ORRERY_AUDIO_LOCAL_WORK_DIR=/absolute/private/speech-work
```

Install compatible `sherpa-onnx`, NumPy and ffmpeg separately. The ASR directory
contains `model.int8.onnx` and `tokens.txt`. TTS contains `model.int8.onnx` (or
`model.onnx`), `tokens.txt`, `lexicon.txt`, the `dict` directory and optionally
`date.fst` and `number.fst`. Retain upstream model license files. These models and
paths are operator configuration; users cannot choose executable or model paths.

Every adapter shares one process slot. Each request loads the required model in
a disposable process, uses two inference threads and has a 90 second wall-time
deadline, 80 second CPU limit and 2 GiB virtual-address limit. There is no resident
model service. Input audio is limited to less than 120 seconds and the existing
16 MiB upload contract. Synthesis accepts up to 1500 characters and returns MP3;
the voice field is empty, `default` or `zh` for the configured Chinese voice.
Timeouts and unsupported inputs fail without claiming successful transcription.

Automatic audio attachment transcription is restricted to private personal
assistant conversations. Tenant, owner and source identity remain required.
Transcripts remain attachment data: they cannot grant permissions or approve an
action. Existing text confirmation and approval-center requirements still apply.
The speech application continues to synthesize only an authorized, persisted
assistant message, never arbitrary caller-supplied reply text.

Before activation, verify pinned model hashes, run real WAV and browser WebM
recognition, synthesize Chinese speech and recognize it again, measure peak RSS,
and run the live attachment dialogue. Do not enable a backend based only on its
configuration or a stubbed test. Preserve the active website, profiles, task
definitions and worker during the API release switch; retain a rollback release.
