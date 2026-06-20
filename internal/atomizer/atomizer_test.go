package atomizer

import "testing"

func TestListItemContent(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"- hello world", "hello world"},
		{"* item", "item"},
		{"+ item", "item"},
		{"1. numbered", "numbered"},
		{"2) numbered paren", "numbered paren"},
		{"42. deep number", "deep number"},
		{"   - indented  ", "indented"},
		{"- [ ] checkbox", "checkbox"},
		{"- [x] done", "done"},
		{"- [X] DONE", "DONE"},
		{"- **bold** `code` item", "bold code item"},
		{"plain text", ""},
		{"", ""},
		{"1.2 not numbered", ""},
		{"-", ""},
	}

	for _, c := range cases {
		got := ListItemContent(c.line)
		if got != c.want {
			t.Errorf("ListItemContent(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}
