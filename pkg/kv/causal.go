package kv

// VersionVector tracks the highest observed counter per actor.
type VersionVector map[string]uint64

// Clone returns a deep copy of the vector.
func (v VersionVector) Clone() VersionVector {
	if len(v) == 0 {
		return nil
	}
	cp := make(VersionVector, len(v))
	for actor, counter := range v {
		cp[actor] = counter
	}
	return cp
}

// Observe records a single actor counter if it is newer than the current one.
func (v VersionVector) Observe(actor string, counter uint64) {
	if actor == "" || counter == 0 {
		return
	}
	if current, ok := v[actor]; !ok || counter > current {
		v[actor] = counter
	}
}

// Merge folds another vector into this one.
func (v VersionVector) Merge(other VersionVector) {
	for actor, counter := range other {
		v.Observe(actor, counter)
	}
}

// Dominates returns true when v is greater than or equal to other for every
// actor present in either vector.
func (v VersionVector) Dominates(other VersionVector) bool {
	for actor, counter := range other {
		if v[actor] < counter {
			return false
		}
	}
	for actor, counter := range v {
		if other[actor] > counter {
			return false
		}
	}
	return true
}

// CausalVersion returns the full observed vector implied by a dot plus context.
func CausalVersion(actor string, counter uint64, context VersionVector) VersionVector {
	full := context.Clone()
	if full == nil {
		full = make(VersionVector)
	}
	full.Observe(actor, counter)
	return full
}

// compareCausal returns:
//
//	 1 when a happened after b
//	-1 when b happened after a
//	 0 when causal ordering is unknown or concurrent
func compareCausal(aActor string, aCounter uint64, aContext VersionVector, bActor string, bCounter uint64, bContext VersionVector) int {
	if aActor == "" || aCounter == 0 || bActor == "" || bCounter == 0 {
		return 0
	}

	aFull := CausalVersion(aActor, aCounter, aContext)
	bFull := CausalVersion(bActor, bCounter, bContext)

	aDominates := aFull.Dominates(bFull)
	bDominates := bFull.Dominates(aFull)

	switch {
	case aDominates && !bDominates:
		return 1
	case bDominates && !aDominates:
		return -1
	default:
		return 0
	}
}
