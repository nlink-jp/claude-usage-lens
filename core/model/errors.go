package model

import "errors"

// ErrNotImplemented marks scaffold stubs whose behaviour lands in Phase 1.
// Returning it (rather than panicking) keeps the whole tree compilable and
// lets the CLI wire commands end-to-end before the internals exist.
var ErrNotImplemented = errors.New("claude-usage-lens: not implemented (scaffold — Phase 1 pending)")
