package media

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestOllamaDescriberSendsTheImageAndReadsTheJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string `json:"model"`
			Think    bool   `json:"think"`
			Messages []struct {
				Images []string `json:"images"`
			} `json:"messages"`
		}
		if r.URL.Path != "/api/chat" || json.NewDecoder(r.Body).Decode(&req) != nil || req.Model != "qwen3.5:4b" ||
			req.Think || len(req.Messages) != 1 || req.Messages[0].Images[0] != "aW1n" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"message":{"content":"{\"caption\":\"A whiteboard\",\"text\":\"orders\"}"}}`)
	}))
	defer srv.Close()
	d := NewOllamaDescriber(srv.URL+"/", "qwen3.5:4b")
	got, err := d.DescribeImage(context.Background(), cortexdb.MediaInput{Data: []byte("img"), MediaType: "image/png"})
	if err != nil || got.Caption != "A whiteboard" || got.Text != "orders" || d.Name() != "ollama/qwen3.5:4b" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestADescriptionThatIsNotJSONIsTheCaption(t *testing.T) {
	if d := parseDescription("A cat on a desk."); d.Caption != "A cat on a desk." || d.Text != "" {
		t.Fatalf("%+v", d)
	}
}

func TestTheTranscriberReadsTimedSegments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil || r.FormValue("response_format") != "verbose_json" || r.FormValue("model") != "whisper-1" {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(file)
		if string(data) != "wav" || !strings.HasSuffix(header.Filename, ".wav") {
			http.Error(w, "bad file", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"text":"hi there","segments":[{"start":0.5,"end":2.25,"text":" hi "},{"start":2.25,"end":3,"text":" "},{"start":3,"end":4.5,"text":"there"}]}`)
	}))
	defer srv.Close()
	tr := NewOpenAITranscriber(srv.URL+"/v1", "whisper-1", "k")
	segs, err := tr.Transcribe(context.Background(), cortexdb.MediaInput{Data: []byte("wav"), MediaType: "audio/wav"})
	if err != nil || len(segs) != 2 || segs[0] != (cortexdb.MediaSegment{StartMS: 500, EndMS: 2250, Text: "hi"}) || segs[1].StartMS != 3000 {
		t.Fatalf("%+v %v", segs, err)
	}
}

func TestATranscriptionWithoutSegmentsIsOneSegment(t *testing.T) {
	segs, err := parseVerboseJSON([]byte(`{"text":" all of it "}`))
	if err != nil || len(segs) != 1 || segs[0].Text != "all of it" {
		t.Fatalf("%+v %v", segs, err)
	}
}

func TestFFmpegTakesAFrameEveryInterval(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not on PATH")
	}
	dir := t.TempDir()
	video := dir + "/test.mp4"
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=duration=5:size=320x240:rate=10",
		"-pix_fmt", "yuv420p", video).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test video: %v %s", err, out)
	}
	data, err := readFile(video)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := NewFFmpegFrameSampler(2*time.Second).SampleFrames(context.Background(), cortexdb.MediaInput{Data: data, MediaType: "video/mp4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 2 || frames[0].AtMS != 0 || frames[1].AtMS != 2000 || frames[0].Image.MediaType != "image/jpeg" || len(frames[0].Image.Data) == 0 {
		t.Fatalf("%d frames: %+v", len(frames), frames)
	}
}

func TestTheMediaEmbedderSendsTheModalityAndADataURI(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string   `json:"model"`
			Input    []string `json:"input"`
			Modality string   `json:"modality"`
		}
		if r.URL.Path != "/embeddings" || json.NewDecoder(r.Body).Decode(&req) != nil || req.Model != "clip" || len(req.Input) != 1 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		seen = append(seen, req.Modality+" "+req.Input[0])
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[0.6,0.8]}]}`)
	}))
	defer srv.Close()
	e := NewOpenAIMediaEmbedder(srv.URL+"/", "clip", "", false)
	ctx := context.Background()
	vecs, err := e.EmbedMedia(ctx, cortexdb.MediaInput{Data: []byte("img"), MediaType: "image/PNG"})
	if err != nil || len(vecs) != 1 || vecs[0].Vector[1] != 0.8 {
		t.Fatalf("%+v %v", vecs, err)
	}
	if v, err := e.EmbedText(ctx, "a heron"); err != nil || len(v) != 2 {
		t.Fatalf("%v %v", v, err)
	}
	if _, err := e.EmbedMedia(ctx, cortexdb.MediaInput{Data: []byte("wav"), MediaType: "audio/wav"}); !errors.Is(err, cortexdb.ErrUnsupportedMedia) {
		t.Fatalf("a model without sound embedded a recording: %v", err)
	}
	if strings.Join(seen, "|") != "image data:image/png;base64,aW1n|text a heron" {
		t.Fatalf("sent %v", seen)
	}
	withSound := NewOpenAIMediaEmbedder(srv.URL, "clip", "", true)
	if _, err := withSound.EmbedMedia(ctx, cortexdb.MediaInput{Data: []byte("wav"), MediaType: "audio/wav"}); err != nil || !strings.HasPrefix(seen[len(seen)-1], "audio data:audio/wav;base64,") {
		t.Fatalf("%v %v", err, seen)
	}
	if _, err := e.EmbedMedia(ctx, cortexdb.MediaInput{Data: []byte("mp4"), MediaType: "video/mp4"}); !errors.Is(err, cortexdb.ErrUnsupportedMedia) {
		t.Fatalf("a video was sent whole: %v", err)
	}
}

func TestOptionsFromEnvTakesTheDeriversThatAreSet(t *testing.T) {
	t.Setenv("CORTEXDB_MEDIA_VISION_URL", "http://localhost:11434")
	t.Setenv("CORTEXDB_MEDIA_VISION_MODEL", "")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("a vision URL without a model was taken")
	}
	t.Setenv("CORTEXDB_MEDIA_VISION_MODEL", "qwen3.5:4b")
	t.Setenv("CORTEXDB_MEDIA_FRAME_INTERVAL", "0")
	t.Setenv("CORTEXDB_MEDIA_MAX_BYTES", "1024")
	opts, err := OptionsFromEnv()
	if err != nil || len(opts) != 2 {
		t.Fatalf("%d options, %v", len(opts), err)
	}
	t.Setenv("CORTEXDB_MEDIA_EMBED_URL", "http://localhost:43181")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("a media embed URL without a model was taken")
	}
	t.Setenv("CORTEXDB_MEDIA_EMBED_MODEL", "clip")
	t.Setenv("CORTEXDB_MEDIA_EMBED_MIN_SCORE", "0.25")
	if opts, err := OptionsFromEnv(); err != nil || len(opts) != 4 {
		t.Fatalf("%d options, %v", len(opts), err)
	}
	t.Setenv("CORTEXDB_MEDIA_EMBED_MIN_SCORE", "2")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("a cosine floor past 1 was taken")
	}
	t.Setenv("CORTEXDB_MEDIA_EMBED_MIN_SCORE", "")
	t.Setenv("CORTEXDB_MEDIA_MAX_BYTES", "lots")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("a bad byte cap was taken")
	}
}

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
