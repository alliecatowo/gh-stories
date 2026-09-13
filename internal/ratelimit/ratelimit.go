// Package ratelimit provides the in-process token buckets used to protect
// login polling, uploads, posts, status calls, replies and reactions.
//
// A single instance keeps these in memory deliberately: it is enough for the
// scale this service is built for, and it avoids adding Redis to the
// deployment. The database enforces the separate, security-critical poll
// budget on pending logins, so a restart cannot reset that one.
package ratelimit

import (
	"sync"
	"time"
)

// Rule is a token-bucket configuration.
type Rule struct {
	// Burst is the maximum number of requests available at once.
	Burst int
	// Per is the window over which Burst tokens are refilled.
	Per time.Duration
}

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is a keyed collection of token buckets.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
	lastGC  time.Time
}

func New(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{buckets: map[string]*bucket{}, now: now, lastGC: now()}
}

// Allow consumes one token for key under rule. It returns whether the request
// may proceed, and how long to wait if not.
func (l *Limiter) Allow(key string, rule Rule) (bool, time.Duration) {
	if rule.Burst <= 0 || rule.Per <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gcLocked(now)

	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: float64(rule.Burst), last: now}
		l.buckets[key] = b
	}
	refillRate := float64(rule.Burst) / rule.Per.Seconds()
	b.tokens += now.Sub(b.last).Seconds() * refillRate
	if b.tokens > float64(rule.Burst) {
		b.tokens = float64(rule.Burst)
	}
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	need := (1 - b.tokens) / refillRate
	return false, time.Duration(need * float64(time.Second))
}

// gcLocked drops idle buckets so a long-running process does not accumulate
// one entry per key it has ever seen.
func (l *Limiter) gcLocked(now time.Time) {
	if now.Sub(l.lastGC) < 5*time.Minute {
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.last) > 15*time.Minute {
			delete(l.buckets, k)
		}
	}
	l.lastGC = now
}

// The rules applied by the API. They are deliberately generous for ordinary
// use and tight for the endpoints an attacker would hammer.
var (
	RuleLoginPoll  = Rule{Burst: 30, Per: time.Minute}
	RuleLoginStart = Rule{Burst: 10, Per: 10 * time.Minute}
	RuleUpload     = Rule{Burst: 20, Per: time.Hour}
	RulePost       = Rule{Burst: 30, Per: time.Hour}
	RuleStatus     = Rule{Burst: 120, Per: time.Minute}
	RuleReply      = Rule{Burst: 60, Per: 10 * time.Minute}
	RuleReaction   = Rule{Burst: 120, Per: 10 * time.Minute}
	RuleRead       = Rule{Burst: 600, Per: time.Minute}
	RuleMutation   = Rule{Burst: 120, Per: time.Minute}
	RuleMedia      = Rule{Burst: 600, Per: time.Minute}
	RuleReport     = Rule{Burst: 10, Per: time.Hour}
)
