package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/i18n"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/serve"

	"golang.org/x/term"
)

func runAgent(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	model := fs.String("model", "", "provider name (default: config default_model)")
	maxSteps := fs.Int("max-steps", 0, "max tool-call rounds (0 = use config/default)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		prompt = readStdin()
	}
	if prompt == "" {
		fmt.Fprintln(os.Stderr, i18n.M.UsageRunHint)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Live run: render the agent's event stream to stdout. Markdown post-stream
	// redraw (cursor moves) is enabled only on a TTY; piped / captured output
	// keeps the raw stream.
	var renderer agent.Renderer
	termW := 80
	if isTTY(os.Stdout) {
		if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
			termW = w
		}
		renderer = newMarkdownRenderer(termW)
	}
	ctrl, err := setup(ctx, *model, *maxSteps, true, agent.NewTextSink(os.Stdout, renderer, termW))
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}
	defer log.CloseSimple("controller", ctrl)

	if err := ctrl.Run(ctx, prompt); err != nil {
		fmt.Fprintln(os.Stderr, "\n"+i18n.M.ErrorPrefix, err)
		return 1
	}
	return 0
}

// runServe exposes the controller over HTTP+SSE: events stream to the browser,
// commands arrive as JSON POSTs. The Broadcaster is the controller's event sink,
// so the same typed stream the chat TUI consumes reaches web clients — the
// transport-agnostic controller driven by a second frontend.
func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	model := fs.String("model", "", "provider name (default: config default_model)")
	maxSteps := fs.Int("max-steps", 0, "max tool-call rounds (0 = use config/default)")
	addr := fs.String("addr", "127.0.0.1:3030", "listen address (use 0.0.0.0:3030 for remote access)")
	tlsCert := fs.String("tls-cert", "", "TLS certificate path (enables HTTPS/WSS)")
	tlsKey := fs.String("tls-key", "", "TLS key path")
	apiKey := fs.String("api-key", "", "API key for authentication (auto-generated if empty)")
	showKey := fs.Bool("show-key", false, "generate and print a new API key then exit")
	resume := fs.String("resume", "", "resume a saved session file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showKey {
		fmt.Println(serve.GenerateAPIKey())
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	bc := serve.NewBroadcaster()
	ctrl, err := setup(ctx, *model, *maxSteps, true, bc)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}
	defer log.CloseSimple("controller", ctrl)

	if *resume != "" {
		loaded, err := agent.LoadSession(*resume)
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		ctrl.Resume(loaded, *resume)
	} else if ctrl.SessionDir() != "" {
		ctrl.SetSessionPath(agent.NewSessionPath(ctrl.SessionDir(), ctrl.Label()))
	}

	// Build server with options
	opts := []serve.ServerOption{}
	key := *apiKey
	if key == "" {
		key = os.Getenv("OK_API_KEY")
	}
	if key != "" {
		opts = append(opts, serve.WithAPIKey(key))
	}
	srv := serve.New(ctrl, bc, opts...)

	scheme := "http"
	if *tlsCert != "" && *tlsKey != "" {
		scheme = "https"
	}
	fmt.Printf("ok serve — %s on %s://%s\n", ctrl.Label(), scheme, *addr)
	if key != "" {
		fmt.Printf("🔑 API Key: %s\n", key)
	}

	if err := srv.RunWith(serve.RunOptions{
		Addr:    *addr,
		TLSCert: *tlsCert,
		TLSKey:  *tlsKey,
	}); err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}
	return 0
}
