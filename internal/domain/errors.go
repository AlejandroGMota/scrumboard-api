package domain

import "errors"

// The error vocabulary of the core. Adapters translate these into their own
// language (HTTP status codes, sync result codes); nothing else about the
// transport reaches this package.
var (
	// ErrNotFound is returned when a well-formed id matches no live row.
	ErrNotFound = errors.New("not found")

	// ErrInvalid is returned when input violates a field-level rule: an empty
	// title, an unknown label, a negative index.
	ErrInvalid = errors.New("invalid")

	// ErrConflict is returned when the same id is reused with different
	// content, or when an update loses to a newer one.
	ErrConflict = errors.New("conflict")

	// ErrWIPExceeded is returned when a card would enter a column that is
	// already at its WIP limit. The only real business rule in the project.
	ErrWIPExceeded = errors.New("wip limit exceeded")

	// ErrColumnNotEmpty is returned when deleting a column that still holds
	// live cards. Cascading that delete is the kind of thing one regrets at
	// eleven at night, so the caller moves or deletes the cards first.
	ErrColumnNotEmpty = errors.New("column still has cards")
)

// FieldError carries which field was wrong alongside ErrInvalid, so the HTTP
// layer can produce a message worth reading without parsing strings.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

// Unwrap makes errors.Is(err, ErrInvalid) true for every field error, so
// callers can either handle the general case or dig for the detail.
func (e *FieldError) Unwrap() error { return ErrInvalid }

// Invalid builds a FieldError. Used all over the domain constructors.
func Invalid(field, message string) error {
	return &FieldError{Field: field, Message: message}
}
