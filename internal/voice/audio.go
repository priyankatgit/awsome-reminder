package voice

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// SampleRate is what whisper.cpp expects: 16 kHz mono.
	SampleRate = 16000
	// MaxSeconds caps one dictation (the frontend stops at 30 s).
	MaxSeconds = 60
	// MinSamples (a quarter second) is the shortest audio worth sending.
	MinSamples = SampleRate / 4
)

// ErrTooShort means the recording was too short to hold any speech.
var ErrTooShort = errors.New("voice: recording too short")

// DecodePCM decodes base64 little-endian 16-bit mono PCM at SampleRate.
func DecodePCM(b64 string) ([]int16, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("voice: audio is not base64: %w", err)
	}
	if len(b)%2 != 0 {
		return nil, errors.New("voice: audio has an odd byte length")
	}
	n := len(b) / 2
	if n < MinSamples {
		return nil, ErrTooShort
	}
	if n > MaxSeconds*SampleRate {
		return nil, fmt.Errorf("voice: recording longer than %d s", MaxSeconds)
	}
	pcm := make([]int16, n)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return pcm, nil
}

// EncodeWAV wraps pcm in a canonical 44-byte RIFF/WAVE header (PCM, mono,
// 16 kHz, 16-bit).
func EncodeWAV(pcm []int16) []byte {
	data := 2 * len(pcm)
	b := make([]byte, 44+data)
	put32 := func(off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }
	put16 := func(off int, v uint16) { binary.LittleEndian.PutUint16(b[off:], v) }
	copy(b[0:], "RIFF")
	put32(4, uint32(36+data))
	copy(b[8:], "WAVEfmt ")
	put32(16, 16)           // fmt chunk size
	put16(20, 1)            // PCM
	put16(22, 1)            // mono
	put32(24, SampleRate)   // sample rate
	put32(28, SampleRate*2) // byte rate
	put16(32, 2)            // block align
	put16(34, 16)           // bits per sample
	copy(b[36:], "data")
	put32(40, uint32(data))
	for i, s := range pcm {
		put16(44+2*i, uint16(s))
	}
	return b
}
