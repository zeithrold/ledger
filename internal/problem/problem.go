// Package problem defines stable application errors without transport dependencies.
package problem

// Kind is a stable problem type, independent of API versions.
type Kind string

// Stable problem kinds shared by all API versions.
const (
	VersionRequired        Kind = "api-version-required"
	VersionInvalid         Kind = "api-version-invalid"
	VersionUnsupported     Kind = "api-version-unsupported"
	InvalidRequest         Kind = "invalid-request"
	AuthenticationRequired Kind = "authentication-required"
	InvalidToken           Kind = "invalid-token"
	UserDisabled           Kind = "user-disabled"
	AccessDenied           Kind = "access-denied"
	MajorUnsupported       Kind = "api-major-version-unsupported"
	NotFound               Kind = "not-found"
	MethodNotAllowed       Kind = "method-not-allowed"
	BootstrapRequired      Kind = "bootstrap-required"
	Conflict               Kind = "accounting-conflict"
	Unavailable            Kind = "service-unavailable"
	Internal               Kind = "internal-error"
)

// FieldError describes a rejected parameter without echoing its value.
type FieldError struct {
	Location string `json:"location"`
	Name     string `json:"name"`
	Reason   string `json:"reason"`
}

// Error carries only safe, client-facing information.
type Error struct {
	Kind   Kind
	Detail string
	Fields []FieldError
	Cause  error
}

func (e *Error) Error() string { return string(e.Kind) }

// New constructs an application error with a stable type.
func New(kind Kind, detail string) *Error { return &Error{Kind: kind, Detail: detail} }
