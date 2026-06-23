// Package cli provides the command-line interface for OK.
//
// review.go — review pending git changes.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/NB-Agent/ok/internal/control"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/winhide"
)

// ReviewChanges runs the agent on the current git diff.
func ReviewChanges(ctx context.Context, ctrl *control.Controller) error {
	diff, err := winhide.Command("git", "diff").Output()
	if err != nil {
		diff2, err2 := winhide.Command("git", "diff", "--cached").Output()
		if err2 != nil {
			return fmt.Errorf("no changes to review: %w", err2)
		}
		diff = diff2
	}
	if len(diff) == 0 {
		fmt.Println("No changes to review.")
		return nil
	}

	prompt := "Review these code changes. Check for bugs, security issues, and style problems:\n\n" +
		"```diff\n" + string(diff) + "\n```"

	fmt.Println("🔍 Reviewing changes...")
	return ctrl.Run(ctx, prompt)
}

// reviewCommand implements the `ok review` CLI command.
func reviewCommand(_ []string, _ string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	sink := event.FuncSink(func(e *event.Event) {
		if e.Kind == event.Text {
			fmt.Print(e.Text)
		}
	})

	ctrl, err := setup(ctx, "", 0, false, sink)
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 1
	}
	defer log.CloseSimple("controller", ctrl)

	if err := ReviewChanges(ctx, ctrl); err != nil {
		fmt.Fprintln(os.Stderr, "review:", err)
		return 1
	}
	return 0
}
