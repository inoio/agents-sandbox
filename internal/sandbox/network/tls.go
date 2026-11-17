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
