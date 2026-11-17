package remnawave

import "errors"

// These errors must never become "not found": provisioning would otherwise
// create another account or select a different subscription on retry.
var (
	ErrAmbiguousAccount     = errors.New("multiple panel accounts: explicit subscription selection required")
	ErrAccountOwnerMismatch = errors.New("panel account owner mismatch")
)
