package backend

import "fmt"

// UnsupportedError is returned instead of a vague failure when an agent asks for
// something this backend does not have. It names the missing capability and the
// nearest thing that would work, because an agent that knows the alternative
// retries usefully, while one that gets "operation failed" retries blindly.
type UnsupportedError struct {
	Backend    string
	Capability string
	// Alternative is prose, not a tool name, because the useful answer is often
	// a different approach rather than a different call.
	Alternative string
}

func (e *UnsupportedError) Error() string {
	msg := fmt.Sprintf("%s backend does not support %s", e.Backend, e.Capability)
	if e.Alternative != "" {
		msg += ". " + e.Alternative
	}
	return msg
}

func Unsupported(backend, capability, alternative string) error {
	return &UnsupportedError{Backend: backend, Capability: capability, Alternative: alternative}
}
