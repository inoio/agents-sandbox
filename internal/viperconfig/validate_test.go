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
