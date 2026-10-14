package builder

import "context"

// ImagePullRef returns the reference microsandbox should pull imageTag from when
// the active backend delivers through the embedded registry, and ok=false when
// the backend delivers through an archive. It ensures the registry daemon is
// running, so the returned authority is live.
func ImagePullRef(ctx context.Context, imageTag string) (string, bool, error) {
	if resolveBackendName() != backendMsb {
		return "", false, nil
	}
	endpoint, err := ensureCacheRegistry(ctx)
	if err != nil {
		return "", false, err
	}
	return endpoint.PullRef(imageTag), true, nil
}

// HoldRegistry keeps the embedded registry's idle timer open for the duration of
// a pull. msb pulls the image inside CreateSandbox, which the registry only sees
// as HTTP blob reads; without a keep-alive the idle reaper could close the
// daemon mid-transfer on a multi-GB image. It returns a stop function; when the
// backend has no registry it returns a no-op.
func HoldRegistry(ctx context.Context) (func(), error) {
	if resolveBackendName() != backendMsb {
		return func() {}, nil
	}
	endpoint, err := ensureCacheRegistry(ctx)
	if err != nil {
		return nil, err
	}
	return endpoint.KeepAlive(ctx), nil
}
