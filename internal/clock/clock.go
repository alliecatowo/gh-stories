// Package clock provides the injectable time source used everywhere expiry
// matters. Production always uses Real; the controllable implementation is
// only reachable from local/test builds, and config refuses it in production.
package clock

import (
	"sync"
	"time"
)

// Clock is the only time source domain code is allowed to consult.
type Clock interface {
	Now() time.Time
}

// Real is authoritative server time in UTC.
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }

// Controllable is a deterministic clock for tests. It lets the acceptance
// suite cross a 24-hour boundary without waiting 24 hours, and without any
// production time-travel endpoint existing.
type Controllable struct {
	mu  sync.RWMutex
	now time.Time
}

func NewControllable(t time.Time) *Controllable {
	return &Controllable{now: t.UTC()}
}

func (c *Controllable) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *Controllable) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d).UTC()
}

// Set moves the clock to an absolute instant.
func (c *Controllable) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t.UTC()
}
