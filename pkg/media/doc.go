// Package media has derivers for cortexdb's ingest_media: an image describer
// over Ollama's chat API, a transcriber over any OpenAI-compatible
// /audio/transcriptions endpoint (OpenAI, whisper.cpp's whisper-server,
// speaches, vLLM), and a frame sampler that runs ffmpeg. They use plain
// net/http and os/exec, no SDK. OptionsFromEnv configures them from the
// environment, as the cortexdb binaries do.
package media
