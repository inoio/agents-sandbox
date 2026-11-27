package viperconfig

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestValidateCPUsRange(t *testing.T) {
	for _, tc := range []struct {
		cpus    int
		wantErr bool
	}{
		{-1, true},
		{0, false},
		{255, false},
		{256, true},
	} {
		v := viper.New()
		v.Set(keyCPUs, tc.cpus)
		err := validate(v)
		if (err != nil) != tc.wantErr {
			t.Errorf("validate cpus=%d err = %v, wantErr %v", tc.cpus, err, tc.wantErr)
		}
	}
}

func TestValidateRejectsNegativeReadyTimeout(t *testing.T) {
	v := viper.New()
	v.Set(keyDockerReadyTimeout, -1*time.Second)
	err := validate(v)
	if err == nil {
		t.Fatal("validate() with a negative docker-ready-timeout should fail")
	}
	if !strings.Contains(err.Error(), keyDockerReadyTimeout) {
		t.Errorf("validate() error = %v, want it to name %q", err, keyDockerReadyTimeout)
	}
}

func TestValidateDNSServersWrapsCause(t *testing.T) {
	entryType := viper.New()
	entryType.Set(keyNetworkDNSServers, 42)
	if err := validateNetworkDNSServers(entryType); err == nil || errors.Unwrap(err) == nil {
		t.Errorf("entries error = %v, want it to wrap its cause", err)
	}

	normalize := viper.New()
	normalize.Set(keyNetworkDNSServers, "1.1.1.1,not a dns server")
	if err := validateNetworkDNSServers(normalize); err == nil || errors.Unwrap(err) == nil {
		t.Errorf("normalize error = %v, want it to wrap its cause", err)
	}
}

func TestValidateNetworkInterceptTLSWrapsCause(t *testing.T) {
	v := viper.New()
	v.Set(keyNetworkInterceptTLS, "sometimes")
	err := validateNetworkInterceptTLS(v)
	if err == nil || errors.Unwrap(err) == nil {
		t.Errorf("intercept-tls error = %v, want it to wrap its cause", err)
	}
}

func TestValidateNetworkTLSBypassWrapsCause(t *testing.T) {
	// A value that stringListEntries cannot read (a scalar where a list is
	// expected) exercises the entries branch.
	entryType := viper.New()
	entryType.Set(keyNetworkTLSBypass, 42)
	if err := validateNetworkTLSBypass(entryType); err == nil || errors.Unwrap(err) == nil {
		t.Errorf("entries error = %v, want it to wrap its cause", err)
	}

	// A well-formed list with an empty entry exercises the ValidateTLSBypass
	// branch.
	emptyEntry := viper.New()
	emptyEntry.Set(keyNetworkTLSBypass, []string{"bypass.example", ""})
	if err := validateNetworkTLSBypass(emptyEntry); err == nil || errors.Unwrap(err) == nil {
		t.Errorf("empty-entry error = %v, want it to wrap its cause", err)
	}
}

func TestDNSServerEntries(t *testing.T) {
	valid := []string{"1.1.1.1", "8.8.8.8:5353"}

	t.Run("empty string rejected", func(t *testing.T) {
		_, err := dnsServerEntries("")
		if err == nil {
			t.Fatal("expected error for empty string")
		}
		if !strings.Contains(err.Error(), "must not be empty") {
			t.Errorf("error %q does not mention empty", err.Error())
		}
	})

	t.Run("comma-separated string split", func(t *testing.T) {
		got, err := dnsServerEntries("1.1.1.1,8.8.8.8:5353")
		if err != nil {
			t.Fatalf("dnsServerEntries: %v", err)
		}
		if len(got) != 2 || got[0] != "1.1.1.1" || got[1] != "8.8.8.8:5353" {
			t.Fatalf("dnsServerEntries = %v, want %v", got, valid)
		}
	})

	t.Run("string slice passed through", func(t *testing.T) {
		got, err := dnsServerEntries(valid)
		if err != nil {
			t.Fatalf("dnsServerEntries: %v", err)
		}
		if len(got) != 2 || got[0] != "1.1.1.1" || got[1] != "8.8.8.8:5353" {
			t.Fatalf("dnsServerEntries = %v, want %v", got, valid)
		}
	})

	t.Run("any slice of strings", func(t *testing.T) {
		got, err := dnsServerEntries([]any{"1.1.1.1", "8.8.8.8:5353"})
		if err != nil {
			t.Fatalf("dnsServerEntries: %v", err)
		}
		if len(got) != 2 || got[0] != "1.1.1.1" || got[1] != "8.8.8.8:5353" {
			t.Fatalf("dnsServerEntries = %v, want %v", got, valid)
		}
	})

	t.Run("non-string entry rejected", func(t *testing.T) {
		if _, err := dnsServerEntries([]any{42}); err == nil {
			t.Fatal("expected error for non-string entry")
		}
	})

	t.Run("unsupported type rejected", func(t *testing.T) {
		if _, err := dnsServerEntries(42); err == nil {
			t.Fatal("expected error for unsupported type")
		}
	})
}

func TestProfileEntries(t *testing.T) {
	got, err := profileEntries("public,host")
	if err != nil {
		t.Fatalf("profileEntries: %v", err)
	}
	if len(got) != 2 || got[0] != "public" || got[1] != "host" {
		t.Fatalf("profileEntries = %v, want [public host]", got)
	}

	if got, err := profileEntries([]string{"public", "host"}); err != nil || len(got) != 2 {
		t.Fatalf("profileEntries([]string) = %v, %v; want [public host], nil", got, err)
	}
	if got, err := profileEntries([]any{"public", "host"}); err != nil || len(got) != 2 {
		t.Fatalf("profileEntries([]any) = %v, %v; want [public host], nil", got, err)
	}
	if _, err := profileEntries([]any{42}); err == nil {
		t.Fatal("profileEntries with a non-string entry should error")
	}
	if _, err := profileEntries(""); err == nil {
		t.Fatal("profileEntries(\"\") should error")
	}
	if _, err := profileEntries(42); err == nil {
		t.Fatal("profileEntries(42) should error")
	}
}

func TestValidateAutoStop(t *testing.T) {
	v := viper.New()
	if err := validateAutoStop(v); err != nil {
		t.Fatalf("validateAutoStop() with unset key should pass, got %v", err)
	}

	v.Set(keyAutoStopMaxSessionRetries, 0)
	if err := validateAutoStop(v); err != nil {
		t.Fatalf("validateAutoStop() with zero retries should pass, got %v", err)
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

// TestValidateReadyTimeouts pins the VM readiness-timeout validation: zero is
// the sentinel for the built-in default and passes, a positive duration passes,
// and a negative value — which would make the readiness deadline expire at once
// — is rejected.
func TestValidateReadyTimeouts(t *testing.T) {
	for _, key := range []string{keyDockerReadyTimeout, keyDaemonReadyTimeout} {
		t.Run(key, func(t *testing.T) {
			v := viper.New()
			if err := validateReadyTimeouts(v); err != nil {
				t.Fatalf("validateReadyTimeouts() with unset key should pass, got %v", err)
			}

			v.Set(key, 0)
			if err := validateReadyTimeouts(v); err != nil {
				t.Fatalf("validateReadyTimeouts() with zero (built-in default) should pass, got %v", err)
			}

			v.Set(key, 90*time.Second)
			if err := validateReadyTimeouts(v); err != nil {
				t.Fatalf("validateReadyTimeouts() with a positive duration should pass, got %v", err)
			}

			v.Set(key, -1*time.Second)
			if err := validateReadyTimeouts(v); err == nil {
				t.Error("validateReadyTimeouts() with a negative duration should fail")
			}
		})
	}
}

// TestValidateMaxLifetime pins the bound's validation: unset is unlimited and
// passes; a positive whole number of seconds passes (including a "7d" string);
// a zero, negative, or sub-second value fails at load.
func TestValidateMaxLifetime(t *testing.T) {
	v := viper.New()
	if err := validateMaxLifetime(v); err != nil {
		t.Fatalf("validateMaxLifetime() with unset key should pass, got %v", err)
	}
	v.Set(keyMaxLifetime, 2*time.Hour)
	if err := validateMaxLifetime(v); err != nil {
		t.Fatalf("validateMaxLifetime() with 2h should pass, got %v", err)
	}
	v.Set(keyMaxLifetime, "7d")
	if err := validateMaxLifetime(v); err != nil {
		t.Fatalf("validateMaxLifetime() with '7d' should pass, got %v", err)
	}
	for _, bad := range []any{0, -1 * time.Second, 500 * time.Millisecond} {
		v.Set(keyMaxLifetime, bad)
		if err := validateMaxLifetime(v); err == nil {
			t.Errorf("validateMaxLifetime() with %v should fail", bad)
		}
	}
}

func TestValidateHomeVolumeOnImageChange(t *testing.T) {
	for value, wantErr := range map[string]bool{"keep": false, "migrate": false, "reset": false, "ask": false, "prompt": true, "": true} {
		v := viper.New()
		v.Set(KeyHomeVolumeOnImageChange, value)
		err := validate(v)
		if (err != nil) != wantErr {
			t.Errorf("validate %s=%q err = %v, wantErr %v", KeyHomeVolumeOnImageChange, value, err, wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "keep, migrate, reset, ask") {
			t.Errorf("validate error %v does not list the accepted answers", err)
		}
	}
}
