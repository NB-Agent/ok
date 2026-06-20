//go:build ignore

package main

import (
	"os"
	"strings"
)

func main() {
	// render.go
	{
		b, _ := os.ReadFile("internal/cli/render.go")
		s := string(b)
		s = strings.Replace(s, "renderTodoPanel() string", "renderTodoPanel(cardW int) string", 1)
		s = strings.Replace(s, "renderApprovalBanner() string", "renderApprovalBanner(cardW int) string", 1)
		s = strings.Replace(s, "w := m.width", "w := cardW", 2)
		s = strings.Replace(s, "todoPanelStyle.Width(m.width)", "todoPanelStyle.Width(cardW)", 1)
		s = strings.Replace(s, "w := m.width - 4", "w := cardW - 4", 1)
		os.WriteFile("internal/cli/render.go", []byte(s), 0644)
	}
	// chooser.go
	{
		b, _ := os.ReadFile("internal/cli/chooser.go")
		s := string(b)
		s = strings.Replace(s, "renderChooser() string", "renderChooser(cardW int) string", 1)
		s = strings.Replace(s, "w := max(m.width, 10)", "w := max(cardW, 10)", 1)
		os.WriteFile("internal/cli/chooser.go", []byte(s), 0644)
	}
	// chat_tui.go
	{
		b, _ := os.ReadFile("internal/cli/chat_tui.go")
		s := string(b)
		s = strings.Replace(s, "m.renderTodoPanel()", "m.renderTodoPanel(cardW)", 1)
		s = strings.Replace(s, "m.renderApprovalBanner()", "m.renderApprovalBanner(cardW)", 1)
		s = strings.Replace(s, "m.renderChooser()", "m.renderChooser(cardW)", 1)
		os.WriteFile("internal/cli/chat_tui.go", []byte(s), 0644)
	}
}
