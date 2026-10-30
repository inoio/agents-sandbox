package opener

import (
	"context"
	"errors"
	"fmt"
)

var errRateLimited = errors.New("too many open requests in the last minute")

// errLoopbackRedirect refuses an OAuth authorization URL whose callback listens
// inside the VM when the callback cannot be forwarded. Opened anyway, the login
// would end on the host's loopback, where nothing listens; worse, agents with a
// paste-code fallback (claude-code) would then present a code that fails the
// token exchange. Refusing makes the agent fall back to its manual or
// device-code login.
var errLoopbackRedirect = errors.New(
	"login callback listens inside the VM and is not reachable from the host browser; " +
		"use the agent's paste-code or device-code login",
)

// NewHostHandler returns the production handler: it classifies the target,
// forwards the login callback of loopback OAuth flows with forwarder (nil
// refuses them), and opens allowed targets with openOnHost.
func NewHostHandler(openOnHost func(target string) error, forwarder *CallbackForwarder) Handler {
	return func(ctx context.Context, target string) error {
		classification := ClassifyTarget(target)
		switch classification.Verdict {
		case VerdictOpen:
			return openOnHost(target)
		case VerdictLoopbackRedirect:
			if forwarder == nil {
				return errLoopbackRedirect
			}
			if err := forwarder.Forward(ctx, classification.Redirect); err != nil {
				return fmt.Errorf("%w (%w)", errLoopbackRedirect, err)
			}
			return openOnHost(target)
		case VerdictRefuse:
		}
		return errors.New(classification.Reason)
	}
}
