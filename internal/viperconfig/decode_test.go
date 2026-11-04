package viperconfig

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"

	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

// FuzzParseHumanDuration drives the `--older-than` duration parser. Its
// documented forms are "7d"/"2w"/"6h"/"30m" (Go's own ParseDuration plus days
// and weeks), and every caller treats the result as an age or an interval, so
// a successful parse must never yield a negative duration — a negative prune
// age matches every artifact. Day/week counts are bounded, so an oversized
// count must be rejected rather than wrap int64.
func FuzzParseHumanDuration(f *testing.F) {
	for _, s := range []string{
		"7d", "2w", "6h", "30m", "1s", "0d", "0", "  3d  ", "1w2d", "d", "5", "1e3d",
		"-1d", "-2w", "-1h", "106751d", "106752d", "110000d", "15251w",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		d, ok := ParseHumanDuration(s)
		if !ok {
			return
		}
		if d < 0 {
			t.Fatalf("ParseHumanDuration(%q) = %v, want a non-negative duration", s, d)
		}
	})
}

func TestParseHumanDuration(t *testing.T) {
	tests := []struct {
		in     string
		want   time.Duration
		wantOK bool
	}{
		{"7d", 7 * 24 * time.Hour, true},
		{"2w", 2 * 7 * 24 * time.Hour, true},
		{"6h", 6 * time.Hour, true},
		{"30m", 30 * time.Minute, true},
		{"90s", 90 * time.Second, true},
		{" 5d ", 5 * 24 * time.Hour, true},
		{"", 0, false},
		{"abc", 0, false},
		{"d", 0, false},
		// Zero stays valid; the "must be > 0" rule belongs to the config
		// validators, not to the parser.
		{"0d", 0, true},
		{"0w", 0, true},
		{"0s", 0, true},
		// Negative durations would invert every age comparison (a negative
		// prune age matches everything), so they are rejected.
		{"-1d", 0, false},
		{"-1w", 0, false},
		{"-1h", 0, false},
		{"-1ns", 0, false},
		// Day/week counts are bounded so the multiply cannot overflow int64
		// into a negative duration.
		{"106751d", 106751 * 24 * time.Hour, true},
		{"106752d", 0, false},
		{"110000d", 0, false},
		{"15250w", 15250 * 7 * 24 * time.Hour, true},
		{"15251w", 0, false},
		{"99999999999999999999d", 0, false},
	}
	for _, tt := range tests {
		got, ok := ParseHumanDuration(tt.in)
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("ParseHumanDuration(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestParseHumanDurationInvalidNumberSuffix(t *testing.T) {
	if _, ok := ParseHumanDuration("xd"); ok {
		t.Error("ParseHumanDuration('xd') should fail")
	}
	if _, ok := ParseHumanDuration("xw"); ok {
		t.Error("ParseHumanDuration('xw') should fail")
	}
}

func TestStringToSliceDecodeHook(t *testing.T) {
	hook := stringToSliceDecodeHook()

	strType := reflect.TypeFor[string]()
	sliceType := reflect.TypeFor[[]string]()
	profileSliceType := reflect.TypeFor[[]network.Profile]()

	exec := func(to reflect.Type, data any) (any, error) {
		return mapstructure.DecodeHookExec(hook, reflect.ValueOf(data), reflect.New(to).Elem())
	}

	t.Run("comma string splits into []string", func(t *testing.T) {
		got, err := exec(sliceType, "a,b")
		if err != nil {
			t.Fatalf("hook error: %v", err)
		}
		if !slices.Equal(got.([]string), []string{"a", "b"}) {
			t.Errorf("hook(a,b) = %v, want [a b]", got)
		}
	})

	t.Run("named string element type splits too", func(t *testing.T) {
		got, err := exec(profileSliceType, "public,host")
		if err != nil {
			t.Fatalf("hook error: %v", err)
		}
		if !slices.Equal(got.([]string), []string{"public", "host"}) {
			t.Errorf("hook(public,host) = %v, want [public host]", got)
		}
	})

	t.Run("empty string yields empty slice", func(t *testing.T) {
		got, err := exec(sliceType, "")
		if err != nil {
			t.Fatalf("hook error: %v", err)
		}
		if len(got.([]string)) != 0 {
			t.Errorf("hook(\"\") = %v, want empty", got)
		}
	})

	t.Run("non-string source passes through", func(t *testing.T) {
		got, err := exec(sliceType, 7)
		if err != nil {
			t.Fatalf("hook error: %v", err)
		}
		if got != 7 {
			t.Errorf("hook(int->slice) = %v, want unchanged", got)
		}
	})

	t.Run("non-slice target passes through", func(t *testing.T) {
		got, err := exec(strType, "a,b")
		if err != nil {
			t.Fatalf("hook error: %v", err)
		}
		if got != "a,b" {
			t.Errorf("hook(string->string) = %v, want unchanged", got)
		}
	})

	t.Run("named string source passes through", func(t *testing.T) {
		src := network.ProfilePublic
		got, err := exec(sliceType, src)
		if err != nil {
			t.Fatalf("hook error: %v", err)
		}
		if got != src {
			t.Errorf("hook(Profile->slice) = %v, want unchanged", got)
		}
	})

	t.Run("non-string element type passes through", func(t *testing.T) {
		got, err := exec(reflect.TypeFor[[]int](), "a,b")
		if err != nil {
			t.Fatalf("hook error: %v", err)
		}
		if got != "a,b" {
			t.Errorf("hook(string->[]int) = %v, want unchanged", got)
		}
	})
}

// durationDecodeHook with a string-kind (but not string) source returns the
// data unchanged because the type assertion to string fails.
func TestDurationDecodeHookNonStringData(t *testing.T) {
	hook := durationDecodeHook()
	durType := reflect.TypeFor[time.Duration]()

	type namedStr string
	exec := func(data any) (any, error) {
		return mapstructure.DecodeHookExec(
			hook, reflect.ValueOf(data), reflect.New(durType).Elem(),
		)
	}

	got, err := exec(namedStr("7d"))
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != namedStr("7d") {
		t.Errorf("hook(namedStr) = %#v, want unchanged", got)
	}
}

// durationDecodeHook with a non-string target (interface) that receives a
// string still routes through the parse helpers.
func TestDurationDecodeHookInterfaceTarget(t *testing.T) {
	hook := durationDecodeHook()
	strType := reflect.TypeFor[string]()
	ifaceType := reflect.TypeFor[any]()

	got, err := mapstructure.DecodeHookExec(
		hook, reflect.ValueOf("7d"), reflect.New(ifaceType).Elem(),
	)
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != 7*24*time.Hour {
		t.Errorf("hook(interface,7d) = %v, want 7d", got)
	}

	// A string that neither helper can parse is returned unchanged.
	got, err = mapstructure.DecodeHookExec(
		hook, reflect.ValueOf("nope"), reflect.New(strType).Elem(),
	)
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != "nope" {
		t.Errorf("hook(string,nope) = %v, want unchanged", got)
	}
}

func TestDurationDecodeHook(t *testing.T) {
	hook := durationDecodeHook()

	durType := reflect.TypeFor[time.Duration]()
	strType := reflect.TypeFor[string]()

	exec := func(_, to reflect.Type, data any) (any, error) {
		return mapstructure.DecodeHookExec(hook, reflect.ValueOf(data), reflect.New(to).Elem())
	}

	// string -> time.Duration decodes "7d"
	got, err := exec(strType, durType, "7d")
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != 7*24*time.Hour {
		t.Errorf("hook(7d) = %v, want 7 days", got)
	}

	// standard duration
	got, err = exec(strType, durType, "5s")
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != 5*time.Second {
		t.Errorf("hook(5s) = %v, want 5s", got)
	}

	// non-duration target returns data unchanged
	got, err = exec(strType, strType, "7d")
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != "7d" {
		t.Errorf("hook string->string = %v, want unchanged", got)
	}

	// non-string source unchanged
	got, err = exec(reflect.TypeFor[int](), durType, 7)
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != 7 {
		t.Errorf("hook int->duration = %v, want unchanged", got)
	}

	// a negative duration: ParseHumanDuration rejects negatives, time.ParseDuration accepts them
	got, err = exec(strType, durType, "-5s")
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != -5*time.Second {
		t.Errorf("hook(-5s) = %v, want -5s", got)
	}

	// unparseable string unchanged
	got, err = exec(strType, durType, "notaduration")
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != "notaduration" {
		t.Errorf("hook(bad) = %v, want unchanged", got)
	}
}

// TestDecodeNotifyReadsEveryKeyIndependently pins that each notify key alone
// configures notifications: setting any single one is enough.
func TestDecodeNotifyReadsEveryKeyIndependently(t *testing.T) {
	off := notify.AudioOff
	cases := []struct {
		name string
		set  func(v *viper.Viper)
		want NotifyConfig
	}{
		{
			name: "no key set",
			set:  func(*viper.Viper) {},
			want: NotifyConfig{Audio: off},
		},
		{
			name: "desktop only",
			set:  func(v *viper.Viper) { v.Set(keyNotifyDesktop, true) },
			want: NotifyConfig{Desktop: true, Audio: off, OnInput: true, OnDone: true, OnError: true},
		},
		{
			name: "audio only",
			set:  func(v *viper.Viper) { v.Set(keyNotifyAudio, string(notify.AudioBell)) },
			want: NotifyConfig{Audio: notify.AudioBell, OnInput: true, OnDone: true, OnError: true},
		},
		{
			name: "on-input only",
			set:  func(v *viper.Viper) { v.Set(keyNotifyOnInput, false) },
			want: NotifyConfig{Audio: off, OnInput: false, OnDone: true, OnError: true},
		},
		{
			name: "on-done only",
			set:  func(v *viper.Viper) { v.Set(keyNotifyOnDone, false) },
			want: NotifyConfig{Audio: off, OnInput: true, OnDone: false, OnError: true},
		},
		{
			name: "on-error only",
			set:  func(v *viper.Viper) { v.Set(keyNotifyOnError, false) },
			want: NotifyConfig{Audio: off, OnInput: true, OnDone: true, OnError: false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			tc.set(v)
			if got := decodeNotify(v); got != tc.want {
				t.Errorf("decodeNotify() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
