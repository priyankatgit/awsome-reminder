package voice

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Model ids (voice.json "model").
const (
	ModelBase  = "base"
	ModelSmall = "small"
	ModelTurbo = "turbo"
	// RecommendedModel is the default for a device that has no model yet.
	RecommendedModel = ModelSmall
)

// modelRevision is the pinned ggerganov/whisper.cpp Hugging Face commit.
const modelRevision = "5359861c739e955e79d9a303bcbc70fb988958b1"

const modelBase = "https://huggingface.co/ggerganov/whisper.cpp/resolve/" + modelRevision + "/"

// ModelSpec is one speech model on offer.
type ModelSpec struct {
	ID    string
	Label string
	Hint  string
	File  string
	Asset Asset
}

// ModelInfo is a ModelSpec as Settings shows it.
type ModelInfo struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Hint        string `json:"hint"`
	Size        int64  `json:"size"`
	Recommended bool   `json:"recommended"`
	Installed   bool   `json:"installed"`
	Active      bool   `json:"active"`
}

// ModelAsset is the English base model (ggml-base.en.bin) at the pinned
// Hugging Face revision; the hash is the file's LFS SHA-256.
var ModelAsset = Asset{
	Name:   "English model",
	URL:    modelBase + "ggml-base.en.bin",
	SHA256: "a03779c86df3323075f5e796cb2ce5029f00ec8869eee3fdfb897afe36c6d002",
	Size:   147964211,
}

// Models is the catalogue, fastest first. Sizes and hashes are the LFS
// values of the pinned revision (checked against full downloads; see the
// voice accuracy spec).
var Models = []ModelSpec{
	{
		ID:    ModelBase,
		Label: "Base",
		Hint:  "Fastest, but often misses words spoken with an accent.",
		File:  "ggml-base.en.bin",
		Asset: ModelAsset,
	},
	{
		ID:    ModelSmall,
		Label: "Small",
		Hint:  "Much more accurate than Base with Indian and other accents, and still quick enough for dictation.",
		File:  "ggml-small.en-q8_0.bin",
		Asset: Asset{
			Name:   "English model (Small)",
			URL:    modelBase + "ggml-small.en-q8_0.bin",
			SHA256: "67a179f608ea6114bd3fdb9060e762b588a3fb3bd00c4387971be4d177958067",
			Size:   264477561,
		},
	},
	{
		ID:    ModelTurbo,
		Label: "Large v3 Turbo",
		Hint:  "The most accurate, but slow without a graphics card: often 15 seconds or more per phrase.",
		File:  "ggml-large-v3-turbo-q5_0.bin",
		Asset: Asset{
			Name:   "speech model (Large v3 Turbo)",
			URL:    modelBase + "ggml-large-v3-turbo-q5_0.bin",
			SHA256: "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2",
			Size:   574041195,
		},
	},
}

// LookupModel returns the catalogue entry for id.
func LookupModel(models []ModelSpec, id string) (ModelSpec, bool) {
	for _, s := range models {
		if s.ID == id {
			return s, true
		}
	}
	return ModelSpec{}, false
}

// MaxWordsLen is the longest "Words I use" list (see the prompt budget).
const MaxWordsLen = 300

// ErrWordsTooLong is returned by NormalizeWords for a list over MaxWordsLen.
var ErrWordsTooLong = fmt.Errorf("voice: keep \"Words I use\" under %d characters", MaxWordsLen)

// NormalizeWords splits s on commas and new lines, trims each entry, drops
// empty ones and case-insensitive duplicates (the first spelling stays) and
// joins them with ", ".
func NormalizeWords(s string) (string, error) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ';' })
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Join(strings.Fields(p), " ")
		if p == "" || seen[strings.ToLower(p)] {
			continue
		}
		seen[strings.ToLower(p)] = true
		out = append(out, p)
	}
	joined := strings.Join(out, ", ")
	if len(joined) > MaxWordsLen {
		return "", ErrWordsTooLong
	}
	return joined, nil
}

const voiceConfigF = "voice.json"

// voiceConfig is voice.json: this device's dictation choices.
type voiceConfig struct {
	Model string `json:"model"`
	Words string `json:"words"`
}

// loadVoiceConfig reads dir/voice.json; ok is false when it is missing or
// unreadable.
func loadVoiceConfig(dir string) (voiceConfig, bool) {
	var c voiceConfig
	b, err := os.ReadFile(filepath.Join(dir, voiceConfigF))
	if err != nil || json.Unmarshal(b, &c) != nil {
		return voiceConfig{}, false
	}
	return c, true
}

// saveVoiceConfig writes dir/voice.json atomically (temp file, then rename).
func saveVoiceConfig(dir string, c voiceConfig) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "voice-*.json.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, voiceConfigF)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
