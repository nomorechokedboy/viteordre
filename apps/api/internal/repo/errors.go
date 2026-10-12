// Package repo is the persistence layer. Repositories own every SQL statement and every file
// operation; the layers above never see a *dbx.DB or a path.
//
// Errors leave this package as *apperr.Error: not-found and conflict conditions get their own
// kind, everything else is Internal with the driver error attached as the cause. The message is
// always safe to show to a client.
package repo

import (
	"errors"
	"fmt"
	"strings"

	"encore.app/internal/apperr"
)

// internal wraps an unexpected failure of the operation op. The cause is kept for logs and
// traces and is never sent to the client.
func internal(op string, err error) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err // already classified further down
	}
	return apperr.Wrap(fmt.Errorf("%s: %w", op, err), apperr.Internal, "internal error")
}

// isUniqueViolation reports whether err is a UNIQUE constraint failure. The Turso driver has no
// typed error for it, so the message is all there is to look at.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}

func errUnexpectedRows(n int64) error { return fmt.Errorf("updated %d rows, want 1", n) }
