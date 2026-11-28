package voice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestModelCatalogPins(t *testing.T) {
	want := map[string]struct {
		file, sha string
		size      int64
	}{
		ModelBase:  {"ggml-base.en.bin", "a03779c86df3323075f5e796cb2ce5029f00ec8869eee3fdfb897afe36c6d002", 147964211},
		ModelSmall: {"ggml-small.en-q8_0.bin", "67a179f608ea6114bd3fdb9060e762b588a3fb3bd00c4387971be4d177958067", 264477561},
		ModelTurbo: {"ggml-large-v3-turbo-q5_0.bin", "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2", 574041195},
	}
	if len(Models) != len(want) {
		t.Fatalf("%d models, want %d", len(Models), len(want))
	}
	for _, m := range Models {
		w, ok := want[m.ID]
		if !ok {
			t.Errorf("unexpected model %q", m.ID)
			continue
		}
		if m.File != w.file || m.Asset.SHA256 != w.sha || m.Asset.Size != w.size {
			t.Errorf("%s = %+v", m.ID, m)
		}
		wantURL := "https://huggingface.co/ggerganov/whisper.cpp/resolve/" + modelRevision + "/" + w.file
		if m.Asset.URL != wantURL || m.Label == "" || m.Hint == "" {
			t.Errorf("%s: url %s label %q hint %q", m.ID, m.Asset.URL, m.Label, m.Hint)
		}
	}
	if _, ok := LookupModel(Models, RecommendedModel); !ok {
		t.Error("the recommended model must be in the catalogue")
	}
}

func TestNormalizeWords(t *testing.T) {
	got, err := NormalizeWords(" Venkatesh ,pune\nPune;  Awesome   Reminder,, ")
	if err != nil || got != "Venkatesh, pune, Awesome Reminder" {
		t.Errorf("NormalizeWords = %q, %v", got, err)
	}
	if got, err := NormalizeWords(""); err != nil || got != "" {
		t.Errorf("empty = %q, %v", got, err)
	}
	if w, err := NormalizeWords(strings.Repeat("क", 150)); err != nil || w == "" {
		t.Errorf("150 Devanagari letters are within the limit: %q, %v", w, err)
	}
	if _, err := NormalizeWords(longWords()); !errors.Is(err, ErrWordsTooLong) {
		t.Errorf("long list: err = %v", err)
	}
}

func TestBuildPrompt(t *testing.T) {
	fixed := strings.Join(promptSentences, " ")
	if len(fixed) > maxPromptLen {
		t.Fatalf("the fixed passage alone (%d) is over the budget", len(fixed))
	}
	if got := BuildPrompt("", ""); got != fixed {
		t.Errorf("bare prompt = %q", got)
	}
	got := BuildPrompt("Venkatesh, Jira", "buy milk")
	if got != fixed+" Words I use: Venkatesh, Jira. buy milk" {
		t.Errorf("prompt = %q", got)
	}
	// Over budget: recent keeps its end, the fixed passage loses sentences
	// from its start, and the whole stays within the budget.
	words := strings.TrimSuffix(strings.Repeat("Shubhangi, ", 27), ", ")
	recent := strings.Repeat("call the bank ", 30) + "tomorrow"
	got = BuildPrompt(words, recent)
	if len(got) > maxPromptLen {
		t.Errorf("prompt is %d chars, want <= %d", len(got), maxPromptLen)
	}
	if !strings.HasSuffix(got, " tomorrow") || !strings.Contains(got, "Words I use: "+words+".") {
		t.Errorf("prompt = %q", got)
	}
	if strings.Contains(got, promptSentences[0]) || !strings.Contains(got, promptSentences[len(promptSentences)-1]) {
		t.Errorf("fixed sentences should drop from the start: %q", got)
	}
	if r := lastChars(strings.Repeat("ab ", 100), maxRecentLen); len(r) > maxRecentLen || strings.HasPrefix(r, " ") || strings.HasPrefix(r, "b") {
		t.Errorf("lastChars = %q", r)
	}
}

func TestDropPromptEcho(t *testing.T) {
	for _, c := range []struct {
		in   string
		ms   int
		want string
	}{
		{"Remind me to call Rahul tomorrow at 9:30 am", 900, ""},
		{"pay the electricity bill by friday high priority", 2000, ""},
		{"Pay the electricity bill by Friday high priority", 3200, "Pay the electricity bill by Friday high priority"},
		{"Pay the electricity bill by Friday high priority", -1, "Pay the electricity bill by Friday high priority"},
		{"Words I use: Venkatesh, Jira", 5000, ""},
		{"Words I use: Venkatesh", -1, ""},
		{"Remind me to call Rahul", 900, "Remind me to call Rahul"},
		{"", 900, ""},
	} {
		if got := DropPromptEcho(c.in, c.ms); got != c.want {
			t.Errorf("DropPromptEcho(%q, %d) = %q, want %q", c.in, c.ms, got, c.want)
		}
	}
}

func TestVoiceConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := loadVoiceConfig(dir); ok {
		t.Error("missing file should not load")
	}
	in := voiceConfig{Model: ModelTurbo, Words: "Venkatesh, Jira"}
	if err := saveVoiceConfig(dir, in); err != nil {
		t.Fatal(err)
	}
	out, ok := loadVoiceConfig(dir)
	if !ok || out != in {
		t.Errorf("loaded %+v, %v", out, ok)
	}
	b, _ := os.ReadFile(filepath.Join(dir, voiceConfigF))
	if string(b) != `{"model":"turbo","words":"Venkatesh, Jira"}` {
		t.Errorf("file = %s", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}
}

// installBase puts the fixture's Base model and a ready engine on disk.
func installBase(t *testing.T, f *fixture) {
	t.Helper()
	m := NewManager(f.cfg)
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "ready")
}

func TestDefaultModel(t *testing.T) {
	t.Run("new device gets the recommended model", func(t *testing.T) {
		f := newFixture(t, "windows", modelBody, nil)
		f.addSmall("")
		m := NewManager(f.cfg)
		st := m.Status()
		if st.Model != ModelSmall || st.Download != f.cfg.EngineAsset.Size+int64(len(turboBody)) {
			t.Errorf("status = %+v", st)
		}
	})
	t.Run("an existing Base install keeps Base", func(t *testing.T) {
		f := newFixture(t, "windows", modelBody, nil)
		installBase(t, f) // catalogue without turbo: Base is the only choice
		f.addSmall("")
		m := NewManager(f.cfg)
		if st := m.Status(); st.Model != ModelBase || st.State != "ready" {
			t.Errorf("status = %+v", st)
		}
		if _, ok := loadVoiceConfig(f.cfg.Dir); ok {
			t.Error("choosing the default must not write voice.json")
		}
	})
	t.Run("voice.json wins; an unknown model falls back", func(t *testing.T) {
		f := newFixture(t, "windows", modelBody, nil)
		f.addSmall("")
		_ = saveVoiceConfig(f.cfg.Dir, voiceConfig{Model: ModelBase, Words: " a ,b,,"})
		m := NewManager(f.cfg)
		if st := m.Status(); st.Model != ModelBase || m.Words() != "a, b" {
			t.Errorf("model %q words %q", st.Model, m.Words())
		}
		_ = saveVoiceConfig(f.cfg.Dir, voiceConfig{Model: "huge"})
		if st := NewManager(f.cfg).Status(); st.Model != ModelSmall {
			t.Errorf("unknown model -> %q", st.Model)
		}
	})
}

func TestSetModelWhileAbsentOnlyRecords(t *testing.T) {
	f := newFixture(t, "windows", modelBody, nil)
	f.addSmall("")
	m := NewManager(f.cfg)
	if err := m.SetModel(ModelBase); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if st.State != "absent" || st.Model != ModelBase || st.Target != "" || st.Download != f.cfg.EngineAsset.Size+f.modelSize() {
		t.Errorf("status = %+v", st)
	}
	if c, _ := loadVoiceConfig(f.cfg.Dir); c.Model != ModelBase {
		t.Errorf("voice.json model = %q", c.Model)
	}
	if err := m.SetModel("nope"); err == nil {
		t.Error("an unknown model must be refused")
	}
	// The install then fetches the chosen model.
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	if err := m.SetModel(ModelSmall); err == nil {
		t.Error("SetModel during the first install must be refused")
	}
	waitState(t, m, "ready")
	if _, err := os.Stat(filepath.Join(f.cfg.Dir, "models", "ggml-base.en.bin")); err != nil {
		t.Error(err)
	}
}

func TestSetModelSwitchesInBackground(t *testing.T) {
	f := newFixture(t, "windows", modelBody, nil)
	installBase(t, f)
	f.addSmall("")
	m := NewManager(f.cfg)
	if _, err := m.Transcribe(context.Background(), pcmB64(make([]int16, MinSamples))); err != nil {
		t.Fatal(err)
	}
	var models []string
	f.cfg.NewEngine = nil // unused: the fixture's NewEngine records the model below
	m.c.NewEngine = func(bin string, server bool, c EngineConfig) Engine {
		models = append(models, filepath.Base(c.Model))
		return f.engine
	}
	if err := m.SetModel(ModelSmall); err != nil {
		t.Fatal(err)
	}
	ok := waitFor(t, 5*time.Second, func() bool { st := m.Status(); return st.Target == "" && st.Model == ModelSmall })
	if !ok {
		t.Fatalf("status = %+v", m.Status())
	}
	sawSwitch := false
	for _, s := range f.seen() {
		if s.Target == ModelSmall {
			sawSwitch = true
			if s.State != "ready" || s.Model != ModelBase || s.Total != int64(len(turboBody)) {
				t.Errorf("switch status = %+v; want ready on Base with turbo's total", s)
			}
		}
	}
	if !sawSwitch {
		t.Error("no status reported the switch")
	}
	if c, _ := loadVoiceConfig(f.cfg.Dir); c.Model != ModelSmall {
		t.Errorf("voice.json model = %q", c.Model)
	}
	if _, err := os.Stat(filepath.Join(f.cfg.Dir, "models", "ggml-base.en.bin")); !os.IsNotExist(err) {
		t.Error("the old model should be removed after the switch")
	}
	if _, err := m.Transcribe(context.Background(), pcmB64(make([]int16, MinSamples))); err != nil {
		t.Fatal(err)
	}
	if strings.Join(models, ",") != "ggml-small.en-q8_0.bin" {
		t.Errorf("engines made for %v, want a new one on the Small model", models)
	}
	info := m.Models()
	if !info[1].Active || !info[1].Installed || info[0].Installed || !info[1].Recommended {
		t.Errorf("models = %+v", info)
	}
}

func TestSetModelSwitchFailureKeepsOldModel(t *testing.T) {
	f := newFixture(t, "windows", modelBody, nil)
	installBase(t, f)
	f.addSmall("/model") // serves the Base body: the size is wrong
	m := NewManager(f.cfg)
	if err := m.SetModel(ModelSmall); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return m.Status().SwitchError != "" })
	st := m.Status()
	if st.State != "ready" || st.Model != ModelBase || st.Target != "" || !strings.HasPrefix(st.SwitchError, "Download failed") {
		t.Errorf("status = %+v", st)
	}
	if _, err := os.Stat(filepath.Join(f.cfg.Dir, "models", "ggml-base.en.bin")); err != nil {
		t.Error("the old model must stay:", err)
	}
	// Choosing the active model again clears the error.
	if err := m.SetModel(ModelBase); err != nil || m.Status().SwitchError != "" {
		t.Errorf("err %v, status %+v", err, m.Status())
	}
}

func TestCancelModelSwitch(t *testing.T) {
	f := newFixture(t, "windows", modelBody, nil)
	installBase(t, f)
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(block); srv.Close() })
	f.addSmall("")
	f.cfg.Models[1].Asset.URL = srv.URL
	f.cfg.Client = srv.Client()
	m := NewManager(f.cfg)
	if err := m.SetModel(ModelSmall); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return m.Status().Target == ModelSmall })
	m.mu.Lock()
	m.c.Models = append(m.c.Models, ModelSpec{ID: ModelTurbo, File: "turbo.bin", Asset: Asset{Size: 1}})
	m.mu.Unlock()
	if err := m.SetModel(ModelTurbo); err == nil || !strings.Contains(err.Error(), "cancel it first") {
		t.Errorf("another model during a switch must be refused: %v", err)
	}
	if err := m.Install(); err == nil {
		t.Error("Install during a switch must be refused")
	}
	m.CancelModelSwitch()
	if !waitFor(t, 5*time.Second, func() bool { return m.Status().Target == "" }) {
		t.Fatal("switch did not stop")
	}
	st := m.Status()
	if st.Model != ModelBase || st.SwitchError != "" || st.State != "ready" {
		t.Errorf("status = %+v", st)
	}
	if left, _ := filepath.Glob(filepath.Join(f.cfg.Dir, "models", "*.part")); len(left) > 0 {
		t.Errorf("partial files left: %v", left)
	}
}

func TestTranscribeUsesWordsAndDropsEcho(t *testing.T) {
	f := newFixture(t, "windows", modelBody, nil)
	installBase(t, f)
	m := NewManager(f.cfg)
	if err := m.SetWords("Venkatesh, Jira"); err != nil {
		t.Fatal(err)
	}
	if c, _ := loadVoiceConfig(f.cfg.Dir); c.Words != "Venkatesh, Jira" || c.Model != ModelBase {
		t.Errorf("voice.json = %+v", c)
	}
	pcm := pcmB64(make([]int16, MinSamples))
	if _, err := m.Transcribe(context.Background(), pcm); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(f.engine.prompt, "Words I use: Venkatesh, Jira.") {
		t.Errorf("prompt = %q", f.engine.prompt)
	}
	f.engine.text = " Remind me to call Rahul tomorrow at 9:30 a.m."
	if got, err := m.TranscribeSegment(context.Background(), pcm, "", 1200); err != nil || got != "" {
		t.Errorf("echo = %q, %v; want dropped", got, err)
	}
	if got, err := m.TranscribeSegment(context.Background(), pcm, "", 3000); err != nil || got == "" {
		t.Errorf("spoken sentence = %q, %v; want kept", got, err)
	}
	f.engine.text = " Words I use: Venkatesh, Jira."
	if got, err := m.Transcribe(context.Background(), pcm); err != nil || got != "" {
		t.Errorf("vocabulary echo = %q, %v; want dropped", got, err)
	}
	if err := m.SetWords(longWords()); !errors.Is(err, ErrWordsTooLong) {
		t.Errorf("long list: %v", err)
	}
	if m.Words() != "Venkatesh, Jira" {
		t.Errorf("a refused list must not replace the saved one: %q", m.Words())
	}
}

// longWords is a list of distinct entries over MaxWordsLen.
func longWords() string {
	var b strings.Builder
	for i := 0; b.Len() <= MaxWordsLen+20; i++ {
		b.WriteString(fmt.Sprintf("name%d, ", i))
	}
	return b.String()
}
