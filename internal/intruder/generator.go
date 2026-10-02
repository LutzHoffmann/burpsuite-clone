package intruder

import "math"

func ValidateConfig(cfg Config) (int64, error) {
	if cfg.Attack != AttackSniper && cfg.Attack != AttackBatteringRam && cfg.Attack != AttackPitchfork && cfg.Attack != AttackClusterBomb {
		return 0, invalid("attack", "unsupported")
	}
	if len(cfg.Template.Raw) > MaxTemplateBytes {
		return 0, invalid("template.raw", "too_large")
	}
	if len(cfg.Positions) == 0 {
		return 0, invalid("positions", "required")
	}
	if cfg.RequestLimit < 1 || cfg.RequestLimit > MaxRequests {
		return 0, invalid("requestLimit", "range")
	}
	if cfg.Concurrency < 1 || cfg.Concurrency > MaxConcurrency {
		return 0, invalid("concurrency", "range")
	}
	if math.IsNaN(cfg.RatePerSecond) || math.IsInf(cfg.RatePerSecond, 0) || cfg.RatePerSecond < MinRatePerSecond || cfg.RatePerSecond > MaxRatePerSecond {
		return 0, invalid("ratePerSecond", "range")
	}
	if cfg.Timeout < MinTimeout || cfg.Timeout > MaxTimeout {
		return 0, invalid("timeout", "range")
	}

	sets := make(map[string]PayloadSet, len(cfg.PayloadSets))
	payloadCount := 0
	for i, set := range cfg.PayloadSets {
		if _, exists := sets[set.ID]; exists {
			return 0, invalid(payloadSetField(i, "id"), "duplicate")
		}
		sets[set.ID] = set
		payloadCount += len(set.Payloads)
		if payloadCount > MaxPayloadCount {
			return 0, invalid("payloadSets", "too_many")
		}
		for payloadIndex, payload := range set.Payloads {
			if len(payload) > MaxPayloadBytes {
				return 0, invalid(payloadValueField(set.ID, payloadIndex), "too_large")
			}
		}
	}

	positionIDs := make(map[string]struct{}, len(cfg.Positions))
	previousEnd := -1
	for i, position := range cfg.Positions {
		field := positionField(i)
		if position.Start < 0 || position.End <= position.Start || position.End > len(cfg.Template.Raw) {
			return 0, invalid(field, "range")
		}
		if i > 0 && position.Start < cfg.Positions[i-1].Start {
			return 0, invalid(field, "order")
		}
		if position.Start < previousEnd {
			return 0, invalid(field, "overlap")
		}
		if _, exists := positionIDs[position.ID]; exists {
			return 0, invalid(field+".id", "duplicate")
		}
		positionIDs[position.ID] = struct{}{}
		set, exists := sets[position.PayloadSetID]
		if !exists {
			return 0, invalid(field+".payloadSetId", "missing")
		}
		if len(set.Payloads) == 0 {
			return 0, invalid("payloadSets["+set.ID+"]", "required")
		}
		previousEnd = position.End
	}

	if cfg.Attack == AttackBatteringRam {
		shared := cfg.Positions[0].PayloadSetID
		for i, position := range cfg.Positions[1:] {
			if position.PayloadSetID != shared {
				return 0, invalid(positionField(i+1)+".payloadSetId", "shared_required")
			}
		}
	}

	count := attackCount(cfg, sets)
	if count < 0 || count > int64(cfg.RequestLimit) {
		return 0, invalid("requestLimit", "exceeded")
	}
	return count, nil
}

func attackCount(cfg Config, sets map[string]PayloadSet) int64 {
	switch cfg.Attack {
	case AttackSniper:
		var count int64
		for _, position := range cfg.Positions {
			count += int64(len(sets[position.PayloadSetID].Payloads))
			if count > int64(cfg.RequestLimit) {
				return -1
			}
		}
		return count
	case AttackBatteringRam:
		return int64(len(sets[cfg.Positions[0].PayloadSetID].Payloads))
	case AttackPitchfork:
		count := len(sets[cfg.Positions[0].PayloadSetID].Payloads)
		for _, position := range cfg.Positions[1:] {
			if length := len(sets[position.PayloadSetID].Payloads); length < count {
				count = length
			}
		}
		return int64(count)
	case AttackClusterBomb:
		count := int64(1)
		for _, position := range cfg.Positions {
			length := int64(len(sets[position.PayloadSetID].Payloads))
			if count > int64(cfg.RequestLimit)/length {
				return -1
			}
			count *= length
		}
		return count
	default:
		return -1
	}
}

func positionField(index int) string {
	return "positions[" + itoa(index) + "]"
}

func payloadSetField(index int, suffix string) string {
	return "payloadSets[" + itoa(index) + "]." + suffix
}

func payloadValueField(id string, index int) string {
	return "payloadSets[" + id + "].payloads[" + itoa(index) + "]"
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}

type Iterator struct {
	cfg      Config
	sets     map[string]PayloadSet
	sequence int64
	total    int64
}

func NewIterator(cfg Config, start int64) (*Iterator, error) {
	total, err := ValidateConfig(cfg)
	if err != nil {
		return nil, err
	}
	if start < 0 || start > total {
		return nil, invalid("startSequence", "range")
	}
	sets := make(map[string]PayloadSet, len(cfg.PayloadSets))
	for _, set := range cfg.PayloadSets {
		sets[set.ID] = set
	}
	return &Iterator{cfg: cfg, sets: sets, sequence: start, total: total}, nil
}

func (i *Iterator) Next() (Combination, bool) {
	if i.sequence >= i.total {
		return Combination{}, false
	}
	sequence := i.sequence
	i.sequence++
	combination := Combination{Sequence: sequence}

	switch i.cfg.Attack {
	case AttackSniper:
		remaining := sequence
		for _, position := range i.cfg.Positions {
			set := i.sets[position.PayloadSetID]
			if remaining < int64(len(set.Payloads)) {
				combination.Selections = []Selection{selection(position, set, int(remaining))}
				return combination, true
			}
			remaining -= int64(len(set.Payloads))
		}
	case AttackBatteringRam:
		set := i.sets[i.cfg.Positions[0].PayloadSetID]
		for _, position := range i.cfg.Positions {
			combination.Selections = append(combination.Selections, selection(position, set, int(sequence)))
		}
	case AttackPitchfork:
		for _, position := range i.cfg.Positions {
			set := i.sets[position.PayloadSetID]
			combination.Selections = append(combination.Selections, selection(position, set, int(sequence)))
		}
	case AttackClusterBomb:
		indexes := make([]int, len(i.cfg.Positions))
		remaining := sequence
		for positionIndex := len(i.cfg.Positions) - 1; positionIndex >= 0; positionIndex-- {
			set := i.sets[i.cfg.Positions[positionIndex].PayloadSetID]
			indexes[positionIndex] = int(remaining % int64(len(set.Payloads)))
			remaining /= int64(len(set.Payloads))
		}
		for positionIndex, position := range i.cfg.Positions {
			set := i.sets[position.PayloadSetID]
			combination.Selections = append(combination.Selections, selection(position, set, indexes[positionIndex]))
		}
	}
	return combination, true
}

func selection(position Position, set PayloadSet, payloadIndex int) Selection {
	payload := make([]byte, len(set.Payloads[payloadIndex]))
	copy(payload, set.Payloads[payloadIndex])
	return Selection{
		PositionID:   position.ID,
		PayloadSetID: set.ID,
		PayloadIndex: payloadIndex,
		Payload:      payload,
	}
}
