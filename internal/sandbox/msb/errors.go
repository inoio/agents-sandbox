package msb

import (
	"errors"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// IsNotFound reports whether err is the microsandbox "sandbox does not exist"
// error, unwrapping any wrapped errors.
func IsNotFound(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrSandboxNotFound)
}

// IsVolumeNotFound reports whether err is the microsandbox "volume does not
// exist" error, unwrapping any wrapped errors. Absence must be distinguished
// from a transient lookup failure: callers that recover or recreate a missing
// resource treat any other error as a failure rather than as absence.
func IsVolumeNotFound(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrVolumeNotFound)
}

// IsVolumeAlreadyExists reports whether err is the microsandbox "volume with
// this name already exists" error, unwrapping any wrapped errors.
func IsVolumeAlreadyExists(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrVolumeAlreadyExists)
}

// IsSandboxAlreadyExists reports whether err is the microsandbox "sandbox with
// this name already exists" error, unwrapping any wrapped errors.
func IsSandboxAlreadyExists(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrSandboxAlreadyExists)
}

// IsReplaced reports whether err indicates that a handle's name now refers to a
// different persisted sandbox identity (the handle was replaced), unwrapping any
// wrapped errors. Operate on live identities and treat a replaced handle as a
// no-op rather than acting on the stranger that now shares the name.
func IsReplaced(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrSandboxReplaced)
}

// IsUnsupportedOperation reports whether err indicates the selected backend or
// native library cannot observe storage (cloud backends and older native
// libraries), unwrapping any wrapped errors. Callers degrade the affected
// categories to unknown rather than reporting zero.
func IsUnsupportedOperation(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrUnsupportedOperation)
}

// IsJobNotRunning reports whether err indicates a detached job is no longer
// running — it already finished, or its runtime generation is gone. The desired
// end state, the job not running, already holds, so a caller treats the error as
// success. It unwraps any wrapped errors.
func IsJobNotRunning(err error) bool {
	var jobErr *msbSdk.JobError
	return errors.As(err, &jobErr) && jobErr.Code == "job_not_running"
}
