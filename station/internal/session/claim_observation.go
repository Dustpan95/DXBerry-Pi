package session

// setClaimLocked preserves identity when the same holder renews a continuous
// claim. A new holder or a claim made after any release/expiry gets a new one.
// Call with o.mu held. Counter exhaustion fails without replacing a live claim.
func (o *Owners) setClaimLocked(radio string, h Holder) error {
	c, exists := o.claims[radio]
	now := o.clock.Now()
	age := now.Sub(c.last)
	generation := c.generation
	if !exists || c.h.ID != h.ID || c.h.Kind != h.Kind || generation == 0 || age < 0 || age >= IdleLimit {
		if o.nextClaim == ^uint64(0) {
			return ErrClaimUnavailable
		}
		o.nextClaim++
		generation = o.nextClaim
	}
	o.claims[radio] = claim{h: h, last: now, generation: generation}
	return nil
}

// TabClaimObservation identifies one continuous tab claim in one Owners
// instance. Its private fields, including the login/tab identity, are not wire
// data. It is local evidence, never authorization for a radio operation.
type TabClaimObservation struct {
	owners     *Owners
	radio      string
	tab        string
	generation uint64
}

// ObserveTabClaim observes only the existing tab claim. It does not claim,
// renew, expire or release anything, run OnDrop, or consult program/PTT sources.
// A preview must separately verify fresh program state, registration and radio
// observations, then bind its own lifetime and login/tab to all of that evidence.
func (o *Owners) ObserveTabClaim(radio, tab string) (TabClaimObservation, bool) {
	if o == nil || tab == "" || radio == "" {
		return TabClaimObservation{}, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	c, ok := o.liveTabClaimLocked(radio, tab)
	if !ok {
		return TabClaimObservation{}, false
	}
	return TabClaimObservation{o, radio, tab, c.generation}, true
}

// TabClaimCurrent checks continuous local ownership only. Release/reclaim by
// the same tab is stale; normal renewal/Touch is not an ownership change.
// This neither holds the claim across a later operation nor checks program or
// hardware state; recall still needs its shared operation boundary.
func (o *Owners) TabClaimCurrent(observation TabClaimObservation) bool {
	if o == nil || observation.owners != o || observation.generation == 0 {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	c, ok := o.liveTabClaimLocked(observation.radio, observation.tab)
	return ok && c.generation == observation.generation
}

func (o *Owners) liveTabClaimLocked(radio, tab string) (claim, bool) {
	c, ok := o.claims[radio]
	if !ok || c.generation == 0 || c.h.Kind != "tab" || c.h.ID != tab || o.clock == nil {
		return claim{}, false
	}
	age := o.clock.Now().Sub(c.last)
	if age < 0 || age >= IdleLimit {
		return claim{}, false
	}
	return c, true
}
