package atomizer

import (
	"testing"
)

func FuzzListItemContent(f *testing.F) {
	f.Add("- hello world")
	f.Add("* item")
	f.Add("1. numbered")
	f.Add("plain text")
	f.Add("")
	f.Add("- **bold** `code`")
	f.Add("[x] checkbox")

	f.Fuzz(func(t *testing.T, line string) {
		// Must never panic on any input, no matter how malformed.
		result := ListItemContent(line)
		_ = result
	})
}
