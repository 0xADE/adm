package dm

import "strings"

const version = "0.1.0"

// buildVersion is set via -ldflags at link time.
var buildVersion string

func getVersion() string {
	tags := strings.Builder{}
	for _, tag := range []string{tagPam, tagUtmp} {
		if tags.Len() > 0 {
			tags.WriteString(", ")
		}
		tags.WriteString(tag)
	}
	if buildVersion != "" {
		if tags.Len() == 0 {
			return buildVersion[1:]
		}
		return buildVersion[1:] + " (" + tags.String() + ")"
	}
	return version
}
