package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// CLIEngine runs whisper-cli once per dictation (used where only the CLI is
// available, e.g. Homebrew on macOS). It holds no process between uses.
type CLIEngine struct {
	c EngineConfig
}

// NewCLIEngine returns a CLI engine.
func NewCLIEngine(c EngineConfig) *CLIEngine { return &CLIEngine{c: c} }

// Warm does nothing: whisper-cli loads the model per run.
func (e *CLIEngine) Warm(context.Context) error { return nil }

// Transcribe writes wav to a temp file and runs whisper-cli on it.
func (e *CLIEngine) Transcribe(ctx context.Context, wav []byte, prompt string) (string, error) {
	f, err := os.CreateTemp("", "awesome-reminder-*.wav")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, werr := f.Write(wav)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", werr
	}

	args := []string{
		"-m", e.c.Model,
		"-t", strconv.Itoa(e.c.Threads),
		"-l", "en",
		"-nt", "-np",
		"-f", f.Name(),
	}
	if prompt != "" {
		args = append(args, "--prompt", prompt)
	}
	cmd := newCmd(ctx, e.c, args...)
	var out bytes.Buffer
	stderr := &tail{max: 4096}
	cmd.Stdout = &out
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("voice: start speech engine: %w", err)
	}
	release := afterStart(cmd.Process)
	err = cmd.Wait()
	release()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) && uint32(ee.ExitCode()) == statusDLLNotFound {
			return "", ErrRuntimeMissing
		}
		return "", fmt.Errorf("voice: speech engine failed: %s", strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

// Running is always false: no process outlives Transcribe.
func (e *CLIEngine) Running() bool { return false }

// Close does nothing.
func (e *CLIEngine) Close() {}
