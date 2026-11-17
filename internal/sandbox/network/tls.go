package network

import (
	"errors"
	"fmt"
	"strings"
)

// InterceptMode selects whether the sandbox's TLS interception is on, off, or
// left to microsandbox's own default.
type InterceptMode string

const (
	// InterceptAuto leaves the decision to microsandbox: it intercepts when it
	// needs to, e.g. to substitute a secret placeholder. The empty value means
	// auto, so an unset config key and an explicit `auto` are the same policy.
	InterceptAuto InterceptMode = "auto"
	InterceptOn   InterceptMode = "on"
	InterceptOff  InterceptMode = "off"
)

// ParseInterceptMode parses an interception mode, erroring on unknown values.
func ParseInterceptMode(s string) (InterceptMode, error) {
	switch InterceptMode(strings.TrimSpace(s)) {
	case "", InterceptAuto:
		return InterceptAuto, nil
	case InterceptOn:
		return InterceptOn, nil
	case InterceptOff:
		return InterceptOff, nil
	default:
		return "", fmt.Errorf("unknown TLS interception mode %q (want auto, on, or off)", s)
	}
}

// ErrInterceptOffWithSecrets is returned when `intercept-tls: off` is combined
// with a configured secret. microsandbox re-enables its own interception to
// substitute secret placeholders, so the runtime cannot honour `off` alongside
// a secret; the launcher rejects the combination rather than claiming to
// disable interception while leaving it on.
var ErrInterceptOffWithSecrets = errors.New(
	"network.intercept-tls: off cannot be combined with secrets: microsandbox " +
		"re-enables interception to substitute secret placeholders; remove the " +
		"secret(s) or use auto/on",
)

// ValidateWithSecrets reports whether the policy can be applied alongside
// secrets. It runs Validate and additionally rejects `intercept-tls: off` when
// a secret is configured. It is the authoritative check the VM path runs before
// it touches a VM (the recreate destroy included); the msb seam keeps a local
// backstop because it cannot import this package.
func (p Policy) ValidateWithSecrets(hasSecrets bool) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.interceptMode() == InterceptOff && hasSecrets {
		return ErrInterceptOffWithSecrets
	}
	return nil
}

// ValidateTLSBypass normalizes TLS-bypass hosts: each entry is trimmed and an
// empty entry is rejected.
func ValidateTLSBypass(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, host := range in {
		host = strings.TrimSpace(host)
		if host == "" {
			return nil, errors.New("empty tls-bypass entry")
		}
		out = append(out, host)
	}
	return out, nil
}
