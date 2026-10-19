package web

import (
	"strings"
	"unicode"

	"playplace/internal/core"
)

// refValue writes ref as one value: the request name, one space, and the
// version. A request name has no spaces, so parseRefValue reads it back. A
// Slack button carries this value, so it binds to the version that its
// message shows. The message never changes, so after an edit or a
// resubmission core refuses the click. The click then never acts on values
// that the clicker did not see.
func refValue(ref core.RequestRef) string { return ref.Name + " " + ref.Version }

// parseRefValue reads a value that refValue wrote: the name runs to the first
// space, and the version is the rest. It gives no ref for an empty value, a
// name with no version, or extra spaces around either part. playplace never
// writes such a value. A button in a message from an older playplace holds
// only the name.
func parseRefValue(v string) (core.RequestRef, bool) {
	name, version, ok := strings.Cut(v, " ")
	if !ok || name == "" || version == "" || strings.ContainsFunc(name, unicode.IsSpace) || strings.TrimSpace(version) != version {
		return core.RequestRef{}, false
	}
	return core.RequestRef{Name: name, Version: version}, true
}
