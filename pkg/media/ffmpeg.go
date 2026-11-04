package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// FFmpegFrameSampler takes one frame every Interval with ffmpeg, scaled to
// at most 768 pixels wide, up to MaxFrames.
type FFmpegFrameSampler struct {
	Interval  time.Duration
	MaxFrames int
	// Binary is the ffmpeg to run, "ffmpeg" on PATH when empty.
	Binary string
}

// NewFFmpegFrameSampler samples a frame every interval (10 s when zero).
func NewFFmpegFrameSampler(interval time.Duration) *FFmpegFrameSampler {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &FFmpegFrameSampler{Interval: interval, MaxFrames: 360}
}

// Name is ffmpeg/<interval>.
func (f *FFmpegFrameSampler) Name() string { return "ffmpeg/" + f.Interval.String() }

// SampleFrames writes the video to a temporary directory (ffmpeg seeks in
// most containers, so it cannot read them from a pipe) and returns the
// frames it takes, each at the instant it was taken.
func (f *FFmpegFrameSampler) SampleFrames(ctx context.Context, video cortexdb.MediaInput) ([]cortexdb.MediaFrame, error) {
	dir, err := os.MkdirTemp("", "cortexdb-frames-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	input := filepath.Join(dir, "input"+extensionOf(video.MediaType))
	if err := os.WriteFile(input, video.Data, 0o600); err != nil {
		return nil, err
	}
	binary := f.Binary
	if binary == "" {
		binary = "ffmpeg"
	}
	maxFrames := f.MaxFrames
	if maxFrames <= 0 {
		maxFrames = 360
	}
	seconds := strconv.FormatFloat(f.Interval.Seconds(), 'f', -1, 64)
	// fps=1/N takes the frame at the middle of each N-second window; the
	// frame of window k is placed at its start, k*N.
	cmd := exec.CommandContext(ctx, binary, "-hide_banner", "-loglevel", "error", "-i", input,
		"-vf", "fps=1/"+seconds+",scale='min(768,iw)':-2", "-frames:v", strconv.Itoa(maxFrames),
		"-q:v", "4", filepath.Join(dir, "frame-%05d.jpg"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, out)
	}
	names, err := filepath.Glob(filepath.Join(dir, "frame-*.jpg"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	frames := make([]cortexdb.MediaFrame, 0, len(names))
	for k, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		frames = append(frames, cortexdb.MediaFrame{
			AtMS:  int64(k) * f.Interval.Milliseconds(),
			Image: cortexdb.MediaInput{Data: data, MediaType: "image/jpeg"},
		})
	}
	return frames, nil
}
