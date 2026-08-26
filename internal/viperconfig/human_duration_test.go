package viperconfig

import (
	"reflect"
	"testing"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

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

	// unparseable string unchanged
	got, err = exec(strType, durType, "notaduration")
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != "notaduration" {
		t.Errorf("hook(bad) = %v, want unchanged", got)
	}
}

func TestFlagTypedDefault(t *testing.T) {
	cpuFlag := &pflag.Flag{Name: "cpus", DefValue: "4"}
	if got := flagTypedDefault("cpus", cpuFlag); got != uint8(4) {
		t.Errorf("flagTypedDefault(cpus) = %#v, want uint8(4)", got)
	}

	boolFlag := &pflag.Flag{Name: "yes", DefValue: "true"}
	if got := flagTypedDefault("yes", boolFlag); got != true {
		t.Errorf("flagTypedDefault(yes) = %#v, want true", got)
	}

	quietFlag := &pflag.Flag{Name: "quiet", DefValue: "true"}
	if got := flagTypedDefault("quiet", quietFlag); got != true {
		t.Errorf("flagTypedDefault(quiet) = %#v, want true", got)
	}
	quietFlagFalse := &pflag.Flag{Name: "quiet", DefValue: "false"}
	if got := flagTypedDefault("quiet", quietFlagFalse); got != false {
		t.Errorf("flagTypedDefault(quiet,false) = %#v, want false", got)
	}

	strFlag := &pflag.Flag{Name: "memory", DefValue: "8G"}
	if got := flagTypedDefault("memory", strFlag); got != "8G" {
		t.Errorf("flagTypedDefault(memory) = %#v, want 8G", got)
	}
}

func TestValidateAutoStop(t *testing.T) {
	v := viper.New()
	if err := validateAutoStop(v); err != nil {
		t.Fatalf("validateAutoStop() with unset key should pass, got %v", err)
	}

	v.Set(keyAutoStopMaxSessionRetries, 5)
	if err := validateAutoStop(v); err != nil {
		t.Fatalf("validateAutoStop() with valid retries should pass, got %v", err)
	}

	v.Set(keyAutoStopMaxSessionRetries, -1)
	if err := validateAutoStop(v); err == nil {
		t.Error("validateAutoStop() with negative retries should fail")
	}
}

func TestValidateAutoStopTimeout(t *testing.T) {
	v := viper.New()
	if err := validateAutoStopTimeout(v); err != nil {
		t.Fatalf("validateAutoStopTimeout() with unset key should pass, got %v", err)
	}

	v.Set(keyAutoStopTimeout, 30*time.Second)
	if err := validateAutoStopTimeout(v); err != nil {
		t.Fatalf("validateAutoStopTimeout() with positive duration should pass, got %v", err)
	}

	// human duration string
	v.Set(keyAutoStopTimeout, "7d")
	if err := validateAutoStopTimeout(v); err != nil {
		t.Fatalf("validateAutoStopTimeout() with '7d' should pass, got %v", err)
	}

	// zero duration fails
	v.Set(keyAutoStopTimeout, 0)
	if err := validateAutoStopTimeout(v); err == nil {
		t.Error("validateAutoStopTimeout() with zero should fail")
	}
}
