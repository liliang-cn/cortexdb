package media

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// OptionsFromEnv configures ingest_media's derivers from the environment:
//
//	CORTEXDB_MEDIA_VISION_URL, CORTEXDB_MEDIA_VISION_MODEL   an Ollama vision model for images and frames
//	CORTEXDB_MEDIA_TRANSCRIBE_URL, CORTEXDB_MEDIA_TRANSCRIBE_MODEL, CORTEXDB_MEDIA_TRANSCRIBE_API_KEY
//	                                                         an OpenAI-compatible transcription endpoint (…/v1)
//	CORTEXDB_MEDIA_EMBED_URL, CORTEXDB_MEDIA_EMBED_MODEL, CORTEXDB_MEDIA_EMBED_API_KEY
//	                                                         a CLIP-style embedding endpoint (infinity's /embeddings with modality)
//	CORTEXDB_MEDIA_EMBED_AUDIO                               1 when that model embeds sound too (CLAP)
//	CORTEXDB_MEDIA_EMBED_MIN_SCORE                           the cosine a media vector must reach to be a hit
//	CORTEXDB_MEDIA_FRAME_INTERVAL                            seconds between video frames (10); 0 takes none
//	CORTEXDB_MEDIA_MAX_BYTES                                 the size cap of one file
//
// Video frames are taken only with a vision model or a media embedder, and
// ffmpeg on PATH.
// Without any of it, ingest_media still takes text a caller derived.
func OptionsFromEnv() ([]cortexdb.Option, error) {
	var opts []cortexdb.Option
	visionURL, visionModel := env("CORTEXDB_MEDIA_VISION_URL"), env("CORTEXDB_MEDIA_VISION_MODEL")
	if (visionURL == "") != (visionModel == "") {
		return nil, fmt.Errorf("CORTEXDB_MEDIA_VISION_URL and CORTEXDB_MEDIA_VISION_MODEL go together")
	}
	if visionURL != "" {
		opts = append(opts, cortexdb.WithImageDescriber(NewOllamaDescriber(visionURL, visionModel)))
	}
	transcribeURL, transcribeModel := env("CORTEXDB_MEDIA_TRANSCRIBE_URL"), env("CORTEXDB_MEDIA_TRANSCRIBE_MODEL")
	if (transcribeURL == "") != (transcribeModel == "") {
		return nil, fmt.Errorf("CORTEXDB_MEDIA_TRANSCRIBE_URL and CORTEXDB_MEDIA_TRANSCRIBE_MODEL go together")
	}
	if transcribeURL != "" {
		opts = append(opts, cortexdb.WithTranscriber(NewOpenAITranscriber(transcribeURL, transcribeModel, env("CORTEXDB_MEDIA_TRANSCRIBE_API_KEY"))))
	}
	embedURL, embedModel := env("CORTEXDB_MEDIA_EMBED_URL"), env("CORTEXDB_MEDIA_EMBED_MODEL")
	if (embedURL == "") != (embedModel == "") {
		return nil, fmt.Errorf("CORTEXDB_MEDIA_EMBED_URL and CORTEXDB_MEDIA_EMBED_MODEL go together")
	}
	if embedURL != "" {
		audio := false
		if v := env("CORTEXDB_MEDIA_EMBED_AUDIO"); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, fmt.Errorf("CORTEXDB_MEDIA_EMBED_AUDIO=%q: want true or false", v)
			}
			audio = b
		}
		opts = append(opts, cortexdb.WithMediaEmbedder(NewOpenAIMediaEmbedder(embedURL, embedModel, env("CORTEXDB_MEDIA_EMBED_API_KEY"), audio)))
		if v := env("CORTEXDB_MEDIA_EMBED_MIN_SCORE"); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f <= 0 || f > 1 {
				return nil, fmt.Errorf("CORTEXDB_MEDIA_EMBED_MIN_SCORE=%q: want a cosine in (0, 1]", v)
			}
			opts = append(opts, cortexdb.WithMediaScoreFloor(f))
		}
	}
	interval := 10 * time.Second
	if v := env("CORTEXDB_MEDIA_FRAME_INTERVAL"); v != "" {
		seconds, err := strconv.ParseFloat(v, 64)
		if err != nil || seconds < 0 {
			return nil, fmt.Errorf("CORTEXDB_MEDIA_FRAME_INTERVAL=%q: want seconds", v)
		}
		interval = time.Duration(seconds * float64(time.Second))
	}
	if (visionURL != "" || embedURL != "") && interval > 0 {
		if _, err := exec.LookPath("ffmpeg"); err == nil {
			opts = append(opts, cortexdb.WithFrameSampler(NewFFmpegFrameSampler(interval)))
		}
	}
	if v := env("CORTEXDB_MEDIA_MAX_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("CORTEXDB_MEDIA_MAX_BYTES=%q: want a positive number of bytes", v)
		}
		opts = append(opts, cortexdb.WithMediaLimits(n, 0))
	}
	return opts, nil
}

func env(key string) string { return strings.TrimSpace(os.Getenv(key)) }
