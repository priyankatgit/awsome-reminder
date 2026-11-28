package voice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
)

// Models lists the speech models for Settings.
func (m *Manager) Models() []ModelInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ModelInfo, len(m.c.Models))
	for i, s := range m.c.Models {
		out[i] = ModelInfo{
			ID: s.ID, Label: s.Label, Hint: s.Hint, Size: s.Asset.Size,
			Recommended: s.ID == RecommendedModel,
			Installed:   m.installedSpec(s),
			Active:      s.ID == m.cfg.Model,
		}
	}
	return out
}

// SetModel chooses the speech model. With nothing installed yet it only
// records the choice (the next consented install fetches it); a model on
// disk becomes active at once; otherwise it is downloaded in the background
// while the old model keeps working, and becomes active (the old file
// removed) once it is in place. Progress arrives through OnStatus
// (Status.Target). Refused during the first install or another switch.
func (m *Manager) SetModel(id string) error {
	spec, ok := LookupModel(m.c.Models, id)
	if !ok {
		return fmt.Errorf("voice: unknown speech model %q", id)
	}
	m.mu.Lock()
	switch {
	case m.installing:
		m.mu.Unlock()
		return errors.New("voice: wait for the download to finish, then choose the model")
	case m.swTarget == id:
		m.mu.Unlock()
		return nil
	case m.swTarget != "":
		m.mu.Unlock()
		return errors.New("voice: another speech model is downloading; cancel it first")
	case m.cfg.Model == id:
		m.swErr = ""
		m.mu.Unlock()
		m.emit(true)
		return nil
	}
	m.swErr = ""
	ready := m.statusLocked().State == "ready"
	if !ready || m.installedSpec(spec) {
		err := m.activateLocked(id)
		m.mu.Unlock()
		if err == nil && ready {
			m.removeOtherModelsUnlocked()
		}
		m.emit(true)
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.swTarget, m.swCancel, m.swDone = id, cancel, 0
	m.mu.Unlock()
	m.emit(true)

	go func() {
		defer cancel()
		err := m.switchTo(ctx, spec)
		m.mu.Lock()
		m.swTarget, m.swCancel, m.swDone = "", nil, 0
		switch {
		case err == nil:
			err = m.activateLocked(id)
			if err != nil {
				m.swErr = "Couldn't save the model choice: " + err.Error()
			}
		case errors.Is(err, context.Canceled):
		default:
			m.swErr = installErrText(err)
			log.Printf("voice: switch model to %s: %v", id, err)
		}
		m.mu.Unlock()
		if err == nil {
			m.removeOtherModelsUnlocked()
		}
		m.emit(true)
	}()
	return nil
}

// switchTo downloads spec's file next to the active model.
func (m *Manager) switchTo(ctx context.Context, spec ModelSpec) error {
	if err := os.MkdirAll(m.modelsDir(), 0o755); err != nil {
		return err
	}
	return Download(ctx, m.c.Client, spec.Asset, m.pathOf(spec), func(done int64) {
		m.mu.Lock()
		m.swDone = done
		m.mu.Unlock()
		m.emit(done == spec.Asset.Size)
	})
}

// activateLocked makes id the active model and saves voice.json; on a save
// error nothing changes. The engine picks the new model at its next use.
func (m *Manager) activateLocked(id string) error {
	next := m.cfg
	next.Model = id
	if err := saveVoiceConfig(m.c.Dir, next); err != nil {
		return fmt.Errorf("voice: save voice settings: %w", err)
	}
	m.cfg = next
	return nil
}

// removeOtherModelsUnlocked is removeOtherModels for a caller without mu.
// An engine still on the old model is left alone (a dictation may be using
// it); engine() replaces it at its next use. whisper.cpp closes the model
// file once loaded, so the old file can go; a failed removal is retried at
// the next start (NewManager).
func (m *Manager) removeOtherModelsUnlocked() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeOtherModels()
}

// CancelModelSwitch stops a background model download; the old model stays.
func (m *Manager) CancelModelSwitch() {
	m.mu.Lock()
	cancel := m.swCancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Words is the "Words I use" list (NormalizeWords form).
func (m *Manager) Words() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.Words
}

// SetWords normalises and saves the "Words I use" list, which every
// transcription then puts in whisper's prompt.
func (m *Manager) SetWords(s string) error {
	w, err := NormalizeWords(s)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.cfg
	next.Words = w
	if err := saveVoiceConfig(m.c.Dir, next); err != nil {
		return fmt.Errorf("voice: save voice settings: %w", err)
	}
	m.cfg = next
	return nil
}
