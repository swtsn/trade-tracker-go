package sqlite

import "strings"

// isUniqueConstraint reports whether err is a SQLite UNIQUE constraint violation.
// It returns false if err is nil.
func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
