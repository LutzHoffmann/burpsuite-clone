package scope

import "sync/atomic"

var emptyRuleSet = &RuleSet{}

type Manager struct {
	current atomic.Pointer[RuleSet]
}

func NewManager(initial *RuleSet) *Manager {
	manager := &Manager{}
	if initial == nil {
		initial = emptyRuleSet
	}
	manager.current.Store(initial)
	return manager
}

func (m *Manager) Current() *RuleSet {
	return m.current.Load()
}

func (m *Manager) Replace(next *RuleSet) {
	if next == nil {
		panic("scope: cannot publish a nil rule set")
	}
	m.current.Store(next)
}
