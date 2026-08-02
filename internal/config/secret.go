package config

// Secret is a config value that must never be shown. Formatting one yields a
// mask, so it stays hidden wherever it ends up: a log line, %v in an error, the
// config view. Reveal is the only way to the real value, which makes the places
// that need it easy to find.
//
// It deliberately does not implement MarshalJSON. Masking on display is safe;
// masking on serialize would silently write bullets over a real key the first
// time anything saves a Config back to disk.
type Secret string

// secretMask is a fixed length: one that tracked the value would leak it
const secretMask = "••••••••"

func (s Secret) String() string {
	if s == "" {
		return "(unset)"
	}
	return secretMask
}

// GoString masks under %#v, which otherwise prints the underlying string
func (s Secret) GoString() string { return s.String() }

// Reveal returns the unmasked value
func (s Secret) Reveal() string { return string(s) }
