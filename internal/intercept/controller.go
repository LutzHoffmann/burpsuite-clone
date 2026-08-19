package intercept

import "sync"

type ControllerState struct {
	Enabled bool   `json:"enabled"`
	Rules   []Rule `json:"rules"`
}

type Controller struct {
	mu    sync.RWMutex
	queue *Queue
	state ControllerState
}

func NewController(queue *Queue, enabled bool, rules []Rule) *Controller {
	if queue == nil {
		queue = NewQueue(0)
	}
	return &Controller{queue: queue, state: ControllerState{Enabled: enabled, Rules: append([]Rule(nil), rules...)}}
}

func (c *Controller) Queue() *Queue {
	return c.queue
}

func (c *Controller) State() ControllerState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ControllerState{Enabled: c.state.Enabled, Rules: append([]Rule(nil), c.state.Rules...)}
}

func (c *Controller) Update(state ControllerState) {
	c.mu.Lock()
	c.state = ControllerState{Enabled: state.Enabled, Rules: append([]Rule(nil), state.Rules...)}
	c.mu.Unlock()
}

func (c *Controller) Matches(request MatchRequest) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.state.Enabled {
		return false
	}
	for _, rule := range c.state.Rules {
		if Matches(rule, request) {
			return true
		}
	}
	return false
}
