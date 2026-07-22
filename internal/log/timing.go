package log

import (
	"time"
)

func NewTiming(l *Logger, enabled bool) (func(string), func()) {
	start := time.Now()
	var phases []struct {
		label   string
		elapsed time.Duration
	}

	tick := func(label string) {
		now := time.Now()
		elapsed := now.Sub(start)
		start = now
		phases = append(phases, struct {
			label   string
			elapsed time.Duration
		}{label, elapsed})
		if enabled {
			l.Timing(label, elapsed)
		}
	}

	summary := func() {
		if !enabled {
			return
		}
		var total time.Duration
		for _, p := range phases {
			total += p.elapsed
		}
		l.Timing("total launcher overhead", total)
	}

	return tick, summary
}
