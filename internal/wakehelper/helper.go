//go:build windows

// Package wakehelper is the Windows "Hey Awesome" listener. sherpa-onnx
// ships no microphone keyword spotter for Windows, so the app runs itself as
// the helper (awesome-reminder.exe --wake-helper ...): it loads the
// sherpa-onnx C API library without cgo, records the default mic with winmm,
// skips silence with an RMS gate, and prints each detection to stderr in the
// stock program's form, `<n>:<json>\n`, which internal/voice parses.
package wakehelper

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// Exit codes.
const (
	exitOK    = 0
	exitUsage = 2
	exitMic   = 3
	exitModel = 4
)

// micSilentText is the error for a mic that delivers only exact zeros
// (ZeroWatch). It exits with exitMic, so the app's supervisor gives up after
// three such exits and shows it in Settings with Try again.
const micSilentText = "the microphone delivers no sound (blocked in Settings › Privacy & security › Microphone, or muted)"

// loopStats counts what one run's loop did, for the measurement test.
type loopStats struct {
	blocks, fedSamples, resets int
}

// lastStats is the latest run's loopStats (read by tests after run returns).
var lastStats loopStats

// Run is the helper's main: args are the arguments after --wake-helper.
func Run(args []string) int {
	return run(args, os.Stdin, os.Stderr)
}

// run is Run with its streams. stdin reaching EOF (the app closed the pipe)
// ends the mic loop; it is not read in --wav mode.
func run(args []string, stdin io.Reader, stderr io.Writer) int {
	fs := flag.NewFlagSet("wake-helper", flag.ContinueOnError)
	fs.SetOutput(stderr)
	libDir := fs.String("lib-dir", "", "folder with "+kwsDLL+" and onnxruntime.dll")
	var m modelPaths
	fs.StringVar(&m.Encoder, "encoder", "", "encoder model (.onnx)")
	fs.StringVar(&m.Decoder, "decoder", "", "decoder model (.onnx)")
	fs.StringVar(&m.Joiner, "joiner", "", "joiner model (.onnx)")
	fs.StringVar(&m.Tokens, "tokens", "", "tokens.txt")
	fs.StringVar(&m.Keywords, "keywords-file", "", "keywords file")
	threads := fs.Int("num-threads", 1, "inference threads")
	wav := fs.String("wav", "", "test only: read this 16 kHz mono 16-bit WAV at full speed instead of the mic")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *libDir == "" || m.Encoder == "" || m.Decoder == "" || m.Joiner == "" || m.Tokens == "" || m.Keywords == "" || *threads < 1 || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "wake helper: usage: --wake-helper --lib-dir DIR --encoder F --decoder F --joiner F --tokens F --keywords-file F [--num-threads N] [--wav F]")
		return exitUsage
	}

	// Read the WAV before the model, so a bad test file is not a model error.
	var samples []int16
	if *wav != "" {
		var err error
		if samples, err = readWav(*wav); err != nil {
			fmt.Fprintf(stderr, "wake helper: wav: %v\n", err)
			return exitUsage
		}
	}

	for _, f := range []string{m.Encoder, m.Decoder, m.Joiner, m.Tokens, m.Keywords} {
		if _, err := os.Stat(f); err != nil {
			fmt.Fprintf(stderr, "wake helper: model: %v\n", err)
			return exitModel
		}
	}
	k, err := loadKWS(*libDir, m, *threads)
	if err != nil {
		fmt.Fprintf(stderr, "wake helper: model: %v\n", err)
		return exitModel
	}
	defer k.close()

	var src func() ([]int16, error)
	var zeros *ZeroWatch // the mic only: a test WAV may hold long digital silence
	if *wav != "" {
		src = wavSource(samples)
	} else {
		// Manual reset. Never closed: the stdin goroutine below may still
		// signal it after Run returns, and the process ends right after.
		stop, err := windows.CreateEvent(nil, 1, 0, nil)
		if err != nil {
			fmt.Fprintf(stderr, "wake helper: microphone: %v\n", err)
			return exitMic
		}
		mc, err := openMic(stop)
		if err != nil {
			fmt.Fprintf(stderr, "wake helper: microphone: %v\n", err)
			return exitMic
		}
		defer mc.close()
		// The one extra goroutine: a blocking read of stdin, which signals
		// stop at EOF (or on any read error, such as no stdin at all).
		go func() {
			if stdin != nil {
				_, _ = io.Copy(io.Discard, stdin)
			}
			windows.SetEvent(stop)
		}()
		src = mc.next
		zeros = NewZeroWatch(micRate)
	}

	gate := NewGate(micRate)
	var st loopStats
	defer func() { lastStats = st }()
	n := 0
	onDetect := func(json string) {
		// One write per detection; os.Stderr is unbuffered.
		fmt.Fprintf(stderr, "%d:%s\n", n, json)
		n++
	}
	for {
		block, err := src()
		if errors.Is(err, io.EOF) || errors.Is(err, errMicStopped) {
			return exitOK
		}
		if err != nil {
			fmt.Fprintf(stderr, "wake helper: microphone: %v\n", err)
			return exitMic
		}
		if zeros != nil && zeros.Push(block) {
			fmt.Fprintf(stderr, "wake helper: microphone: %s\n", micSilentText)
			return exitMic
		}
		feed, reset := gate.Push(block)
		k.feed(feed, onDetect)
		st.blocks++
		st.fedSamples += len(feed)
		if reset {
			st.resets++
			if err := k.restart(); err != nil {
				fmt.Fprintf(stderr, "wake helper: model: %v\n", err)
				return exitModel
			}
		}
	}
}

// wavTail is the silence fed after a WAV file, standing in for the quiet
// room after the phrase: longer than the gate's 1.5 s hang, so the gate
// feeds the end of the phrase and then closes, as it would on the mic.
const wavTail = 2 * micRate

// wavSource yields samples in mic-sized blocks, then wavTail of silence,
// then io.EOF.
func wavSource(samples []int16) func() ([]int16, error) {
	samples = append(samples, make([]int16, wavTail)...)
	return func() ([]int16, error) {
		if len(samples) == 0 {
			return nil, io.EOF
		}
		n := min(micBufLen, len(samples))
		b := samples[:n]
		samples = samples[n:]
		return b, nil
	}
}

// readWav reads a 16 kHz mono 16-bit PCM WAV file.
func readWav(path string) ([]int16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%s: not a WAV file", path)
	}
	var fmtOK bool
	for p := 12; p+8 <= len(b); {
		id, size := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:p+8]))
		p += 8
		if size < 0 || p+size > len(b) {
			size = len(b) - p // tolerate a truncated last chunk
		}
		body := b[p : p+size]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, fmt.Errorf("%s: short fmt chunk", path)
			}
			tag, ch := binary.LittleEndian.Uint16(body[0:2]), binary.LittleEndian.Uint16(body[2:4])
			rate, bits := binary.LittleEndian.Uint32(body[4:8]), binary.LittleEndian.Uint16(body[14:16])
			if tag != waveFormatPCM || ch != 1 || rate != micRate || bits != 16 {
				return nil, fmt.Errorf("%s: want 16 kHz mono 16-bit PCM, got format %d, %d ch, %d Hz, %d bit", path, tag, ch, rate, bits)
			}
			fmtOK = true
		case "data":
			if !fmtOK {
				return nil, fmt.Errorf("%s: data before fmt", path)
			}
			s := make([]int16, len(body)/2)
			for i := range s {
				s[i] = int16(binary.LittleEndian.Uint16(body[2*i:]))
			}
			return s, nil
		}
		p += size + size&1 // chunks are word aligned
	}
	return nil, fmt.Errorf("%s: no fmt and data chunks", path)
}
