package events

import "strings"

// Event types. A type doubles as the NATS subject the event is published on,
// so the dotted naming is load-bearing: the "mods.>", "errors.>" and
// "notifications.>" wildcards
// a consumer subscribes to depend on it.
const (
	TypeModDiscovered = "mods.discovered"

	// TypeNotificationSend carries a ready-made message for the notifier.
	TypeNotificationSend = "notifications.send"
)

// errorTypePrefix namespaces per-service error types under a single wildcard.
const errorTypePrefix = "errors."

// Sources. The service that produced an event, carried in Event.Source.
const (
	SourceTracker  = "tracker"
	SourceNotifier = "notifier"
)

// ErrorType returns the event type for an error reported by service,
// e.g. ErrorType(SourceTracker) == "errors.tracker".
//
// Errors are namespaced per service rather than sharing one "error" type so
// that a consumer can filter on "errors.>" for all of them, or on a single
// service, without the payload having to be decoded first.
func ErrorType(service string) string {
	return errorTypePrefix + service
}

// IsErrorType reports whether typ is an error event type. Notifier routing
// uses it to send every error to the admin channel regardless of service.
func IsErrorType(typ string) bool {
	return strings.HasPrefix(typ, errorTypePrefix)
}
