package hotkey

import "sync"

// Registrar registers c as a global hotkey, calling onPress on each key-down.
// The returned function releases the registration.
type Registrar func(c Combo, onPress func()) (unregister func(), err error)

// Manager owns at most one registered global hotkey. It is safe for
// concurrent use.
type Manager struct {
	reg     Registrar
	onPress func()

	mu    sync.Mutex
	cur   string // canonical form of the registered combo, "" if none
	unreg func()
	// setErr is the last Set's error (the saved combo isn't registered);
	// only Set changes it. resumeErr is the last failed Resume's error.
	setErr    error
	resumeErr error
	// suspended: cur is released for now (the settings window is recording
	// a new combo) and Resume registers it again.
	suspended bool
}

func NewManager(reg Registrar, onPress func()) *Manager {
	return &Manager{reg: reg, onPress: onPress}
}

// Set switches the hotkey to combo.
//
// The new combo is registered first; only if that succeeds is the old one
// released. If combo cannot be parsed or registered, the previously working
// hotkey stays registered, the error is returned and recorded in Err(). A
// later successful Set clears Err(). Setting the combo that is already
// registered does nothing except clear Err().
func (m *Manager) Set(combo string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := ParseCombo(combo)
	if err != nil {
		m.setErr = err
		return err
	}
	if s := c.String(); m.unreg != nil && s == m.cur {
		m.setErr = nil
		return nil
	}
	unreg, err := m.reg(c, m.onPress)
	if err != nil {
		m.setErr = err
		return err
	}
	if m.unreg != nil {
		m.unreg()
	}
	m.cur, m.unreg, m.setErr, m.resumeErr, m.suspended = c.String(), unreg, nil, nil, false
	return nil
}

// Suspend releases the registered hotkey until Resume, so its keys reach the
// focused window (the settings hotkey recorder). It does nothing if no hotkey
// is registered or it is already suspended.
func (m *Manager) Suspend() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unreg == nil {
		return
	}
	m.unreg()
	m.unreg, m.suspended = nil, true
}

// Resume registers the suspended hotkey again. It does nothing unless
// suspended. If registering fails the error is returned and recorded in
// Err(), and the hotkey stays suspended so a later Resume can retry. A
// successful Resume clears only that error: an earlier failed Set (the saved
// combo is still not registered) stays reported.
func (m *Manager) Resume() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.suspended {
		return nil
	}
	c, err := ParseCombo(m.cur)
	if err != nil {
		m.resumeErr = err
		return err
	}
	unreg, err := m.reg(c, m.onPress)
	if err != nil {
		m.resumeErr = err
		return err
	}
	m.unreg, m.resumeErr, m.suspended = unreg, nil, false
	return nil
}

// Err returns the error from the last Set, else from a failed Resume, or nil.
func (m *Manager) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setErr != nil {
		return m.setErr
	}
	return m.resumeErr
}

// Close releases the registered hotkey, if any.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unreg != nil {
		m.unreg()
	}
	m.cur, m.unreg, m.setErr, m.resumeErr, m.suspended = "", nil, nil, nil, false
}
