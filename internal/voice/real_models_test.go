package voice

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestRealModels runs the real whisper-server on real clips, for measuring
// accuracy and speed by hand. It is skipped unless AR_VOICE_BENCH names a
// folder holding whisper-server(.exe) (anywhere below it), the model files
// (any of the catalogue's) and *.wav clips (16 kHz mono); AR_VOICE_WORDS is
// the "Words I use" list. Each clip's transcript and time are logged (-v).
func TestRealModels(t *testing.T) {
	dir := os.Getenv("AR_VOICE_BENCH")
	if dir == "" {
		t.Skip("set AR_VOICE_BENCH to measure the real models")
	}
	exe := "whisper-server"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	var bin string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == exe && bin == "" {
			bin = p
		}
		return nil
	})
	if bin == "" {
		t.Fatalf("no %s under %s", exe, dir)
	}
	clips, _ := filepath.Glob(filepath.Join(dir, "*", "*.wav"))
	more, _ := filepath.Glob(filepath.Join(dir, "*.wav"))
	clips = append(clips, more...)
	words, _ := NormalizeWords(os.Getenv("AR_VOICE_WORDS"))
	for _, spec := range Models {
		var model string
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Name() == spec.File {
				model = p
			}
			return nil
		})
		if model == "" {
			continue
		}
		e := NewServerEngine(EngineConfig{Bin: bin, Model: model, Threads: DefaultThreads(runtime.NumCPU()), StartTimeout: 2 * time.Minute})
		start := time.Now()
		if err := e.Warm(context.Background()); err != nil {
			t.Fatalf("%s: %v", spec.ID, err)
		}
		t.Logf("%s: loaded in %v", spec.ID, time.Since(start).Round(time.Millisecond))
		for _, c := range clips {
			b, err := os.ReadFile(c)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			raw, err := e.Transcribe(context.Background(), b, BuildPrompt(words, ""))
			if err != nil {
				t.Fatalf("%s %s: %v", spec.ID, c, err)
			}
			t.Logf("%s %s (%v): %q", spec.ID, filepath.Base(c), time.Since(start).Round(time.Millisecond), CleanTranscript(raw))
		}
		e.Close()
	}
}
