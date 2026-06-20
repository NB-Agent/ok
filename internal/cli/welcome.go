package cli

import (
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/i18n"
)

// welcome is the zero-arg landing screen: it reports config and key readiness,
// then guides the user to the next concrete step.
func welcome(version string) int {
	src := config.SourcePath()

	// First run on an interactive terminal: actively guide setup rather than
	// printing a static screen and exiting. interactiveSetup owns the language
	// prompt and welcome banner so every prompt the user sees is already
	// localized to their choice.
	if src == "" && isInteractive() {
		if rc := interactiveSetup("ok.toml"); rc != 0 {
			return rc
		}
		// Config just written; reload so .env (and any pinned language) is
		// picked up. If the chosen provider's key is ready, drop into chat.
		if cfg, err := config.Load(); err == nil && cfg.Validate(cfg.DefaultModel) == nil {
			if cfg.Language != "" {
				i18n.DetectLanguage(cfg.Language)
			}
			fmt.Printf("\n"+i18n.M.StartingChatFmt+"\n\n", bold("ok chat"))
			return chatREPL(nil)
		}
		fmt.Println("\n" + i18n.M.SetKeyHint)
		return 0
	}

	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		cfg = config.Default()
	}

	// ok.toml exists and parses on a terminal: go into chat. If any enabled
	// provider's key isn't set yet, re-run the wizard's key-entry step inline
	// — first run already chose language and providers, so we don't re-ask
	// those. Skipping the prompts is still fine; the chat banner falls back to
	// a one-line warning.
	if src != "" && cfgErr == nil && isInteractive() {
		if rc := promptMissingKeys(cfg); rc != 0 {
			return rc
		}
		return chatREPL(nil)
	}

	var b strings.Builder
	b.WriteString(boxed([]string{
		accent("◆") + " " + bold("ok") + "  " + dim(version),
		dim(i18n.M.Subtitle),
	}))

	switch {
	case src == "":
		fmt.Fprintf(&b, "\n  %s %s\n", padRight(i18n.M.ConfigLabel, 8), dim(i18n.M.ConfigNotFound))
	case cfgErr != nil:
		fmt.Fprintf(&b, "\n  %s %s\n", padRight(i18n.M.ConfigLabel, 8), yellow(fmt.Sprintf(i18n.M.ConfigErrorFmt, src, cfgErr)))
	default:
		fmt.Fprintf(&b, "\n  %s %s\n", padRight(i18n.M.ConfigLabel, 8), src)
	}

	ready := 0
	for i, p := range cfg.Providers {
		label := i18n.M.ModelsLabel
		if i > 0 {
			label = ""
		}
		dot, status := yellow("●"), dim(i18n.M.NoKey)
		if p.APIKey() != "" {
			dot, status = green("●"), green(i18n.M.Ready)
			ready++
		}
		fmt.Fprintf(&b, "  %s %s %s%s\n", padRight(label, 8), dot, padRight(p.Name, 16), status)
	}

	fmt.Fprintf(&b, "\n  %s %s\n", accent("▌"), bold(i18n.M.GetStarted))
	n := 1
	step := func(cmd, desc string) {
		fmt.Fprintf(&b, "    %s  %s %s\n", accent(fmt.Sprint(n)), padRight(cmd, 16), dim(desc))
		n++
	}
	if src == "" {
		step("ok setup", i18n.M.StepScaffold)
	}
	if ready == 0 {
		step(i18n.M.StepSetKey, i18n.M.StepSetKeyHint)
	}
	step("ok chat", i18n.M.StepChatDesc)
	step(`ok run "task"`, i18n.M.StepRunDesc)

	fmt.Fprintf(&b, "\n  %s\n", dim(i18n.M.HelpFooter))

	// Environment summary — on every launch, so the user always knows the state.
	summary := DoctorSummary()
	if summary != "" {
		fmt.Fprintf(&b, "\n  %s\n", summary)
	}

	fmt.Print(b.String())
	return 0
}
