package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/NB-Agent/ok/internal/i18n"
	"github.com/NB-Agent/ok/internal/plugin"
)

func (m *chatTUI) prompts() []plugin.Prompt {
	if m.host == nil {
		return nil
	}
	return m.host.Prompts()
}

func (m *chatTUI) runSlashCommand(input string) tea.Cmd {
	cmd := strings.TrimSpace(strings.SplitN(input, " ", 2)[0])

	if strings.HasPrefix(cmd, "/mcp__") {
		return m.runMCPPrompt(input)
	}

	switch cmd {
	case "/compact":
		if err := m.ctrl.Compact(context.Background()); err != nil {
			m.notice(fmt.Sprintf("%s: %v", i18n.M.SlashCompactFailed, err))
			return nil
		}
		m.notice(i18n.M.SlashCompactDone)
		if err := m.ctrl.Snapshot(); err != nil {
			fmt.Fprintf(os.Stderr, "cli: snapshot after compact: %v\n", err)
		}
	case "/new":
		if err := m.ctrl.NewSession(); err != nil {
			m.notice(fmt.Sprintf("%s: %v", i18n.M.SlashNewFailed, err))
			return nil
		}
		m.pending.Reset()
		m.reasoning.Reset()
		m.todoArgs = ""
		m.chooser = nil
		m.commitLine("")
		m.commitLine(strings.TrimRight(renderTUIBanner(m.label, "", m.width), "\n"))
		m.notice(i18n.M.SlashNewDone)
	case "/todo":
		m.todoArgs = ""
		m.notice(i18n.M.SlashTodoCleared)
	case "/dst":
		m.runDSTCommand(input)
	case "/mcp":
		m.runMCPSubcommand(input)
	case "/model":
		return m.runModelSubcommand(input)
	case "/skill", "/skills":
		m.runSkillSubcommand(input)
	case "/hooks":
		m.runHooksSubcommand(input)
	case "/help":
		m.notice(i18n.M.SlashHelp)
		if names := m.commandNames(); names != "" {
			m.notice("custom: " + names)
		}
	case "/memory":
		m.showMemory()
	default:
		if sent, ok := m.ctrl.CustomCommand(input); ok {
			return m.startTurn(m.ctrl.Compose(sent), input)
		}
		if sent, ok := m.ctrl.RunSkill(input); ok {
			return m.startTurn(m.ctrl.Compose(sent), input)
		}
		m.notice(fmt.Sprintf("%s: %s", i18n.M.SlashUnknown, cmd))
	}
	return nil
}

func (m *chatTUI) commandNames() string {
	if len(m.commands) == 0 {
		return ""
	}
	names := make([]string, len(m.commands))
	for i, c := range m.commands {
		names[i] = "/" + c.Name
	}
	return strings.Join(names, " · ")
}

func (m *chatTUI) runDSTCommand(input string) {
	args := tokenizeArgs(input)
	sub := ""
	if len(args) >= 2 {
		sub = args[1]
	}
	switch sub {
	case "on":
		if !m.ctrl.IsDSTAvailable() {
			m.notice("DST: not available (hooks not initialized)")
		} else {
			m.ctrl.SetDSTEnabled(true)
			m.notice("DST: on — per-step verification active")
		}
	case "off":
		m.ctrl.SetDSTEnabled(false)
		m.notice("DST: off — per-step verification disabled")
	case "status", "":
		if m.ctrl.DSTEnabled() {
			m.notice("DST: on — compile/test checks + proof chain active")
		} else {
			m.notice("DST: off")
		}
	default:
		m.notice("DST: unknown subcommand " + sub + " — use /dst on|off|status")
	}
}

func (m *chatTUI) runMCPSubcommand(input string) {
	args := tokenizeArgs(input)
	if len(args) < 2 {
		m.showMCPStatus()
		return
	}
	switch args[1] {
	case "list", "ls":
		m.showMCPStatus()
	case "add":
		entry, err := parseMCPAdd(args[2:])
		if err != nil {
			m.notice(err.Error())
			return
		}
		n, err := m.ctrl.AddMCPServer(entry)
		if err != nil {
			m.notice("mcp add: " + err.Error())
			return
		}
		m.notice(fmt.Sprintf("connected %s — %d tools, saved to config (available next message)", entry.Name, n))
	case "remove", "rm":
		if len(args) < 3 {
			m.notice("usage: /mcp remove <name>")
			return
		}
		name := args[2]
		disconnected, err := m.ctrl.RemoveMCPServer(name)
		if err != nil {
			m.notice("mcp remove: " + err.Error())
			return
		}
		if disconnected {
			m.notice("disconnected " + name + " and removed it from config")
		} else {
			m.notice("removed " + name + " from config")
		}
	default:
		m.notice("unknown /mcp subcommand " + args[1] + " — try: /mcp, /mcp add, /mcp remove")
	}
}

func (m *chatTUI) showMCPStatus() {
	if m.host == nil || len(m.host.Servers()) == 0 {
		m.notice(i18n.M.SlashMCPNone)
		return
	}
	servers := m.host.Servers()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", dim(fmt.Sprintf("  · MCP servers (%d)", len(servers))))
	for _, s := range servers {
		fmt.Fprintf(&b, "    %s %s %s\n", accent("✓"), bold(s.Name),
			dim(fmt.Sprintf("(%s) — %d tools · %d prompts · %d resources", s.Transport, s.Tools, s.Prompts, s.Resources)))
	}
	for _, p := range m.host.Prompts() {
		fmt.Fprintf(&b, "      %s  %s\n", "/"+p.Name, dim(p.Description))
	}
	for _, r := range m.host.Resources() {
		label := r.Name
		if label == "" {
			label = r.Description
		}
		fmt.Fprintf(&b, "      %s  %s\n", "@"+r.Server+":"+r.URI, dim(label))
	}
	m.commitLine(strings.TrimRight(b.String(), "\n"))
}

func (m *chatTUI) notice(note string) {
	m.commitLine(dim("  · " + note))
}

func (m *chatTUI) resolveRefs(line string) tea.Cmd {
	return func() tea.Msg {
		block, errs := m.ctrl.ResolveRefs(context.Background(), line)
		return refsResolvedMsg{line: line, block: block, errs: errs}
	}
}

func (m *chatTUI) runMCPPrompt(input string) tea.Cmd {
	return func() tea.Msg {
		sent, found, err := m.ctrl.MCPPrompt(context.Background(), input)
		if !found {
			name := strings.TrimPrefix(strings.Fields(input)[0], "/")
			return promptResolvedMsg{display: input, err: fmt.Errorf("%s: /%s", i18n.M.SlashUnknown, name)}
		}
		return promptResolvedMsg{display: input, sent: sent, err: err}
	}
}
