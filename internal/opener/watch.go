package opener

import (
	"context"
	"sync"
	"time"
)

// Timing of the spool protocol. The shim waits shimResponseTimeout (see the
// shim script) for a response, so a request older than staleRequestAge has
// been given up on and must not be opened late — that also covers requests
// written while no launcher was attached. Leftover claims and responses are
// swept after staleLeftoverAge.
const (
	defaultPollInterval = 200 * time.Millisecond // gomutants:disable INTEGER_DECREMENT,INTEGER_INCREMENT reason="a poll cadence, not a contract; tests set PollInterval, so the default's exact value is unobservable"
	staleRequestAge     = 15 * time.Second       // gomutants:disable INTEGER_DECREMENT,INTEGER_INCREMENT reason="an age policy keyed to the shim's timeout, not a contract; tests use the constant and cannot see the exact value"
	staleLeftoverAge    = 2 * time.Minute        // gomutants:disable INTEGER_DECREMENT,INTEGER_INCREMENT reason="an age policy, not a contract; tests use the constant and cannot see the exact value"
)

// Rate bounds against a runaway guest process: at most maxRequestsInFlight
// requests are served at once, and at most maxOpensPerMinute targets reach the
// host handler per minute.
const (
	maxRequestsInFlight = 8 // gomutants:disable INTEGER_DECREMENT,INTEGER_INCREMENT reason="a concurrency bound, not a contract; the burst test uses the constant"
	maxOpensPerMinute   = 20
)

// Response lines understood by the shim.
const (
	responseOpened  = "ok"
	responseRefused = "refused\t"
)

// Handler decides about one target and acts on it. A non-nil error refuses the
// target; its message is shown to the in-VM caller and in the session report.
type Handler func(ctx context.Context, target string) error

// Watcher serves the spool directory of one home volume.
//
//exhaustruct:ignore
type Watcher struct {
	HomeVolumePath string
	Handle         Handler
	PollInterval   time.Duration
	now            func() time.Time

	// inFlight bounds the requests being served concurrently. Requests are
	// served concurrently because a host handler may take up to
	// hostOpenSettleTime; served one after another, a burst would push later
	// answers past the shim's timeout although their links opened.
	inFlight chan struct{}
	serving  sync.WaitGroup

	mutex       sync.Mutex
	recentOpens []time.Time
	report      Report
}

// Watch serves requests until ctx is done and returns what was refused.
func (w *Watcher) Watch(ctx context.Context) (Report, error) {
	spoolDirectory, err := openSpool(w.HomeVolumePath)
	if err != nil {
		return Report{}, err
	}
	defer spoolDirectory.close()
	ticker := time.NewTicker(effectivePollInterval(w.PollInterval))
	defer ticker.Stop()
	for {
		w.poll(ctx, spoolDirectory)
		if !waitForTick(ctx, ticker.C) {
			w.serving.Wait()
			return w.report, nil
		}
	}
}

// effectivePollInterval returns interval, or the default when it is unset.
func effectivePollInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return defaultPollInterval
	}
	return interval
}

// waitForTick waits for the next poll and reports false once ctx is done.
func waitForTick(ctx context.Context, tick <-chan time.Time) bool {
	select {
	case <-ctx.Done():
		return false
	case <-tick:
		return true
	}
}

func (w *Watcher) initialize() {
	if w.now == nil {
		w.now = time.Now
	}
	if w.inFlight == nil {
		w.inFlight = make(chan struct{}, maxRequestsInFlight)
	}
}

// poll sweeps stale files and starts serving pending requests, as many as
// there is room for in flight; the rest wait for a later poll.
func (w *Watcher) poll(ctx context.Context, spoolDirectory *spool) {
	w.initialize()
	// entries returns nil on error, so a failed read simply leaves nothing to do.
	entries, _ := spoolDirectory.entries()
	for _, entry := range entries {
		age := w.now().Sub(entry.modified)
		switch {
		case entry.suffix == requestSuffix && age > staleRequestAge:
			spoolDirectory.remove(entry)
		case age > staleLeftoverAge:
			// A request this old was already swept above, so only leftover
			// claims and responses reach here.
			spoolDirectory.remove(entry)
		case entry.suffix == requestSuffix:
			w.startServing(ctx, spoolDirectory, entry.identifier)
		}
	}
}

func (w *Watcher) startServing(ctx context.Context, spoolDirectory *spool, identifier string) {
	select {
	case w.inFlight <- struct{}{}:
	default:
		return
	}
	if !spoolDirectory.claim(identifier) {
		<-w.inFlight
		return
	}
	w.serving.Go(func() {
		defer func() { <-w.inFlight }()
		w.serve(ctx, spoolDirectory, identifier)
	})
}

func (w *Watcher) serve(ctx context.Context, spoolDirectory *spool, identifier string) {
	target, err := spoolDirectory.readClaimed(identifier)
	if err == nil {
		err = w.handleWithinRate(ctx, target)
	}
	if err != nil {
		reason := sanitize(err.Error())
		w.mutex.Lock()
		w.report.addRefusal(target, reason)
		w.mutex.Unlock()
		spoolDirectory.respond(identifier, responseRefused+reason)
		return
	}
	spoolDirectory.respond(identifier, responseOpened)
}

func (w *Watcher) handleWithinRate(ctx context.Context, target string) error {
	if !w.reserveOpen() {
		return errRateLimited
	}
	return w.Handle(ctx, target)
}

// reserveOpen records an open unless maxOpensPerMinute were already recorded
// in the last minute.
func (w *Watcher) reserveOpen() bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	now := w.now()
	recent := w.recentOpens[:0]
	for _, opened := range w.recentOpens {
		if now.Sub(opened) < time.Minute {
			recent = append(recent, opened)
		}
	}
	w.recentOpens = recent
	if len(w.recentOpens) >= maxOpensPerMinute {
		return false
	}
	w.recentOpens = append(w.recentOpens, now)
	return true
}
