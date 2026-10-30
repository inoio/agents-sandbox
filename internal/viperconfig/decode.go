package viperconfig

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/notify"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Bounds for the day/week forms, derived from the multiplier so the multiply
// cannot wrap int64 into a negative duration.
const (
	nanosecondsPerDay = int64(24 * time.Hour)
	maxDays           = math.MaxInt64 / nanosecondsPerDay
	maxWeeks          = math.MaxInt64 / (7 * nanosecondsPerDay)
)

// ParseHumanDuration parses duration strings like "7d", "2w", "6h", "30m"
// into time.Duration. Go's time.ParseDuration supports ns/us/ms/s/m/h
// but not "d" (days) or "w" (weeks).
//
// Negative durations are rejected: callers use the result as a prune age or an
// interval, where a negative value silently inverts the comparison — a
// negative prune age matches every artifact. Oversized day/week counts are
// rejected for the same reason, since the multiply would wrap negative.
func ParseHumanDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasSuffix(s, "w"):
		// A bit size of 63 only differs for values that overflow it, which
		// also exceed maxWeeks, so both branches reject them.
		// gomutants:disable-next-line INTEGER_DECREMENT reason="values needing bit 63 also exceed maxWeeks"
		num, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil || num < 0 || num > maxWeeks {
			return 0, false
		}
		return time.Duration(num) * 7 * 24 * time.Hour, true
	case strings.HasSuffix(s, "d"):
		// A bit size of 63 only differs for values that overflow it, which
		// also exceed maxDays, so both branches reject them.
		// gomutants:disable-next-line INTEGER_DECREMENT reason="values needing bit 63 also exceed maxDays"
		num, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil || num < 0 || num > maxDays {
			return 0, false
		}
		return time.Duration(num) * 24 * time.Hour, true
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

// stringToSliceDecodeHook splits a comma-separated string into a slice whose
// element kind is string. It generalizes mapstructure's StringToSliceHookFunc
// (which fires only for []string) so named string types such as network.Profile
// decode the same way from env vars and StringSlice flags.
func stringToSliceDecodeHook() mapstructure.DecodeHookFunc {
	return func(_ reflect.Type, t reflect.Type, data any) (any, error) {
		if t.Kind() != reflect.Slice {
			return data, nil
		}
		if t.Elem().Kind() != reflect.String {
			return data, nil
		}
		raw, ok := data.(string)
		if !ok {
			return data, nil
		}
		if raw == "" {
			return []string{}, nil
		}
		return strings.Split(raw, ","), nil
	}
}

func durationDecodeHook() mapstructure.DecodeHookFunc {
	return func(_ reflect.Type, t reflect.Type, data any) (any, error) {
		if t.Kind() != reflect.Interface && t != reflect.TypeFor[time.Duration]() {
			return data, nil
		}
		// A non-string value leaves str empty, which no parser accepts, so
		// the data is returned unchanged below.
		str, _ := data.(string)
		if d, ok := ParseHumanDuration(str); ok {
			return d, nil
		}
		if d, err := time.ParseDuration(str); err == nil {
			return d, nil
		}
		return data, nil
	}
}

// decodeNotify reads the notify: section from dotted viper keys. It returns an
// inactive config when no notify key is set; otherwise channels are read as-is
// and triggers default to true.
func decodeNotify(v *viper.Viper) NotifyConfig {
	cfg := NotifyConfig{Audio: notify.AudioOff}
	if !v.IsSet(keyNotifyDesktop) && !v.IsSet(keyNotifyAudio) &&
		!v.IsSet(keyNotifyOnInput) && !v.IsSet(keyNotifyOnDone) && !v.IsSet(keyNotifyOnError) {
		return cfg
	}
	cfg.Desktop = v.GetBool(keyNotifyDesktop)
	if s := v.GetString(keyNotifyAudio); s != "" {
		if m, err := notify.ParseAudioMode(s); err == nil {
			cfg.Audio = m
		}
	}
	cfg.OnInput = true
	cfg.OnDone = true
	cfg.OnError = true
	if v.IsSet(keyNotifyOnInput) {
		cfg.OnInput = v.GetBool(keyNotifyOnInput)
	}
	if v.IsSet(keyNotifyOnDone) {
		cfg.OnDone = v.GetBool(keyNotifyOnDone)
	}
	if v.IsSet(keyNotifyOnError) {
		cfg.OnError = v.GetBool(keyNotifyOnError)
	}
	return cfg
}
