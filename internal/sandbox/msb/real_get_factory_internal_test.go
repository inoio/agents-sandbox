package msb

// realGetFactory captures the package's default Client factory before TestMain
// installs the fail-fast mock, so tests can exercise the real closure body.
var realGetFactory = Get
