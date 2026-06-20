package cli

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/sandbox"

	"golang.org/x/term"
	"github.com/NB-Agent/ok/internal/i18n"
)

func doctor() string {
	var b strings.Builder
	b.WriteString(boxed([]string{
		accent("◆") + " " + bold("ok doctor"),
		dim(i18n.M.Subtitle),
	}))
	b.WriteString("\n")

	b.WriteString("\n  " + bold("System") + "\n")
	writeCheck(&b, true, "Go", strings.TrimPrefix(runtime.Version(), "go"))
	writeCheck(&b, true, "OS", runtime.GOOS+"/"+runtime.GOARCH)
	it := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if it {
		writeCheck(&b, true, "Terminal", "interactive")
	} else {
		writeCheck(&b, false, "Terminal", "non-interactive (scripts/CI)")
	}

	b.WriteString("\n  " + bold("Configuration") + "\n")
	src := config.SourcePath()
	if src == "" {
		writeCheck(&b, false, "ok.toml", i18n.M.ConfigNotFound)
		fmt.Fprintf(&b, "       %s\n", dim("run `ok setup` to create one"))
	} else if cfg, err := config.Load(); err != nil {
		writeCheck(&b, false, "ok.toml", fmt.Sprintf("parse error: %v", err))
	} else {
		writeCheck(&b, true, "ok.toml", src)
		fmt.Fprintf(&b, "       %s %s\n", padRight("model", 16), dim(cfg.DefaultModel))
		if cfg.Agent.PlannerModel != "" {
			fmt.Fprintf(&b, "       %s %s\n", padRight("planner", 16), dim(cfg.Agent.PlannerModel))
		}
	}

	b.WriteString("\n  " + bold("API Keys") + "\n")
	if cfg, _ := config.Load(); cfg != nil {
		ready := 0
		for _, p := range cfg.Providers {
			dot, status := yellow("●"), dim(i18n.M.NoKey)
			if p.APIKey() != "" {
				dot, status = green("●"), green(i18n.M.Ready)
				ready++
			}
			fmt.Fprintf(&b, "  %s  %s %s%s\n", dot, padRight(p.Name, 16), padRight(dim(p.Kind+"/"+p.Model), 20), status)
		}
		if ready == 0 && len(cfg.Providers) > 0 {
			fmt.Fprintf(&b, "       %s\n", dim("set via `ok setup` or add to .env"))
		}
	}

	b.WriteString("\n  " + bold("Sandbox") + "\n")
	if sandbox.Available() {
		writeCheck(&b, true, "Sandbox", "available \u2014 commands run confined")
	} else {
		writeCheck(&b, false, "Sandbox", "unavailable \u2014 commands run unconfined")
	}

	b.WriteString("\n  " + bold("Permissions") + "\n")
	if cfg, _ := config.Load(); cfg != nil {
		writeRules(&b, "allow", cfg.ModeAllow())
		writeRules(&b, "ask", cfg.ModeAsk())
		writeRules(&b, "deny", cfg.ModeDeny())
	}
	if cfg, _ := config.Load(); cfg == nil || (len(cfg.ModeAllow()) == 0 && len(cfg.ModeAsk()) == 0 && len(cfg.ModeDeny()) == 0) {
		fmt.Fprintf(&b, "       %s\n", dim("none \u2014 all tools permitted in current mode"))
	}

	return b.String()
}

func DoctorSummary() string {
	warnings := 0
	if _, err := config.Load(); err != nil {
		warnings++
	}
	if !sandbox.Available() {
		warnings++
	}
	if cfg, _ := config.Load(); cfg != nil {
		hasKey := false
		for _, p := range cfg.Providers {
			if p.APIKey() != "" {
				hasKey = true
				break
			}
		}
		if !hasKey {
			warnings++
		}
	}
	if warnings == 0 {
		return green("\u2713") + " " + dim("environment ready")
	}
	return yellow(fmt.Sprintf("\u26a0  %d issue(s) found \u2014 run `ok doctor` for details", warnings))
}

func writeCheck(b *strings.Builder, ok bool, label, status string) {
	dot := green("\u2713")
	if !ok {
		dot = yellow("\u223c")
	}
	fmt.Fprintf(b, "  %s  %s %s\n", dot, padRight(label, 16), dim(status))
}

// writeRules writes a labeled list of path-level allow/ask/deny rules.
// If the list is empty it prints nothing — the caller handles the "none" case.
func writeRules(b *strings.Builder, label string, rules []string) {
	if len(rules) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s %s\n", dim(label), dim("("+strconv.Itoa(len(rules))+" rules)"))
	for _, r := range rules {
		fmt.Fprintf(b, "       %s\n", dim(r))
	}
}
