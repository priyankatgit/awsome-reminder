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
	err   error
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
		m.err = err
		return err
	}
	if s := c.String(); m.unreg != nil && s == m.cur {
		m.err = nil
		return nil
	}
	unreg, err := m.reg(c, m.onPress)
	if err != nil {
		m.err = err
		return err
	}
	if m.unreg != nil {
		m.unreg()
	}
	m.cur, m.unreg, m.err = c.String(), unreg, nil
	return nil
}

// Err returns the error from the last Set, or nil.
func (m *Manager) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// Close releases the registered hotkey, if any.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unreg != nil {
		m.unreg()
	}
	m.cur, m.unreg, m.err = "", nil, nil
}
