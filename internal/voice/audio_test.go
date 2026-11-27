package voice

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"testing"
)

func TestEncodeWAVHeader(t *testing.T) {
	w := EncodeWAV([]int16{0, 1, -1})
	if len(w) != 50 {
		t.Fatalf("len = %d, want 50", len(w))
	}
	le32 := func(off int) uint32 { return binary.LittleEndian.Uint32(w[off:]) }
	le16 := func(off int) uint16 { return binary.LittleEndian.Uint16(w[off:]) }
	if string(w[0:4]) != "RIFF" || le32(4) != 42 || string(w[8:16]) != "WAVEfmt " {
		t.Errorf("riff header = %q", w[:16])
	}
	if le32(16) != 16 || le16(20) != 1 || le16(22) != 1 || le32(24) != 16000 || le32(28) != 32000 || le16(32) != 2 || le16(34) != 16 {
		t.Errorf("fmt chunk = % x", w[16:36])
	}
	if string(w[36:40]) != "data" || le32(40) != 6 {
		t.Errorf("data header = % x", w[36:44])
	}
	if !bytes.Equal(w[44:], []byte{0x00, 0x00, 0x01, 0x00, 0xff, 0xff}) {
		t.Errorf("samples = % x", w[44:])
	}
}

func pcmB64(samples []int16) string {
	b := make([]byte, 2*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(s))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestDecodePCM(t *testing.T) {
	in := make([]int16, MinSamples)
	pattern := []int16{0, 32767, -32768, 5}
	for i := range in {
		in[i] = pattern[i%len(pattern)]
	}
	got, err := DecodePCM(pcmB64(in))
	if err != nil {
		t.Fatalf("DecodePCM: %v", err)
	}
	if len(got) != len(in) || got[1] != 32767 || got[2] != -32768 || got[3] != 5 {
		t.Errorf("round trip mismatch: len %d, head %v", len(got), got[:4])
	}

	if _, err := DecodePCM(pcmB64(make([]int16, 10))); !errors.Is(err, ErrTooShort) {
		t.Errorf("10 samples: err = %v, want ErrTooShort", err)
	}
	if _, err := DecodePCM(base64.StdEncoding.EncodeToString([]byte{1, 2, 3})); err == nil {
		t.Error("odd byte length should fail")
	}
	if _, err := DecodePCM("%%%not base64"); err == nil {
		t.Error("invalid base64 should fail")
	}
	if _, err := DecodePCM(pcmB64(make([]int16, MaxSeconds*SampleRate+1))); err == nil {
		t.Error("more than MaxSeconds should fail")
	}
}
