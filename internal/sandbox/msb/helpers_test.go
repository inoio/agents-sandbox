package msb

import (
	"errors"
)

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	return 0, errors.New("boom")
}
