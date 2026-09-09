package intercept

import "sync"

type ControllerState struct {
	Enabled          bool              `json:"enabled"`
	Rules            []Rule            `json:"rules"`
	ResponseEnabled  bool              `json:"responseEnabled"`
	ResponseRules    []Rule            `json:"responseRules"`
	ReplacementRules []ReplacementRule `json:"replacementRules"`
}

type Controller struct {
	mu            sync.RWMutex
	queue         *Queue
	responseQueue *Queue
	state         ControllerState
}

func NewController(queue *Queue, enabled bool, rules []Rule) *Controller {
	if queue == nil {
		queue = NewQueue(0)
	}
	return &Controller{queue: queue, responseQueue: NewQueue(queue.timeout), state: ControllerState{Enabled: enabled, Rules: append([]Rule(nil), rules...), ResponseRules: []Rule{{Enabled: true}}}}
}

func (c *Controller) ResponseQueue() *Queue { return c.responseQueue }

func cloneState(s ControllerState) ControllerState {
	s.Rules = append([]Rule(nil), s.Rules...)
	s.ResponseRules = append([]Rule(nil), s.ResponseRules...)
	s.ReplacementRules = append([]ReplacementRule(nil), s.ReplacementRules...)
	return s
}

func (c *Controller) Queue() *Queue {
	return c.queue
}

func (c *Controller) State() ControllerState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneState(c.state)
}

func (c *Controller) Update(state ControllerState) {
	if ValidateState(state) != nil {
		return
	}
	c.mu.Lock()
	c.state = cloneState(state)
	c.mu.Unlock()
}

func (c *Controller) MatchesResponse(request MatchRequest) bool {
	s := c.State()
	if !s.ResponseEnabled {
		return false
	}
	for _, rule := range s.ResponseRules {
		if Matches(rule, request) {
			return true
		}
	}
	return false
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
