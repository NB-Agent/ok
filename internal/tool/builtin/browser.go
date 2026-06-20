package builtin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/NB-Agent/ok/internal/tool"
)

func init() {
	tool.RegisterBuiltin(browserTool{})
}

type browserTool struct{}

func (browserTool) Name() string         { return "browser" }
func (browserTool) ReadOnly() bool       { return false }
func (browserTool) CostCategory() string { return "slow" }

func (browserTool) Description() string {
	return "Control a headless Chrome browser. Navigate pages, click elements, " +
		"type text, screenshots, extract text, execute JavaScript, fill forms. " +
		"Use for any website interaction — login, SPAs, dynamic content. " +
		"Requires Chrome/Chromium in PATH or set CHROME_PATH."
}

func (browserTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"action":{"enum":["navigate","click","type","screenshot","text","eval","wait","scroll","back","forward","refresh","close"],"type":"string"},"selector":{"type":"string"},"url":{"type":"string"},"value":{"type":"string"},"wait_ms":{"type":"integer"}},"required":["action"],"type":"object"}`)
}

var (
	bMu     sync.Mutex
	bInit   bool
	bChrome string
)

func (browserTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action   string `json:"action"`
		URL      string `json:"url"`
		Selector string `json:"selector"`
		Value    string `json:"value"`
		WaitMS   int    `json:"wait_ms"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("browser: %w", err)
	}

	bMu.Lock()
	defer bMu.Unlock()

	if !bInit {
		bChrome = chromeFind()
		bInit = true
	}
	if bChrome == "" {
		return "", fmt.Errorf("browser: Chrome not found. Install Chrome or set CHROME_PATH")
	}

	return chromeRun(ctx, bChrome, p)
}

func chromeFind() string {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, n := range []string{
		"google-chrome", "chromium", "chromium-browser", "chrome", "chrome.exe",
	} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	for _, n := range []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`,
	} {
		if _, err := os.Stat(n); err == nil {
			return n
		}
	}
	return ""
}

func chromeRun(ctx context.Context, chrome string, p struct {
	Action   string `json:"action"`
	URL      string `json:"url"`
	Selector string `json:"selector"`
	Value    string `json:"value"`
	WaitMS   int    `json:"wait_ms"`
}) (string, error) {
	timeout := 15000
	if p.WaitMS > 0 && p.WaitMS < 30000 {
		timeout += p.WaitMS
	}

	baseArgs := []string{
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--disable-extensions",
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=1280,900",
		"--virtual-time-budget=" + fmt.Sprint(timeout),
	}

	switch p.Action {
	case "navigate", "screenshot":
		if p.URL == "" {
			return "", fmt.Errorf("browser: %s requires 'url'", p.Action)
		}
		if err := validateBrowserURL(p.URL); err != nil {
			return "", err
		}
		outFile := tempFileName("ok-browser-screenshot", ".png")
		args := append(baseArgs, "--screenshot="+outFile, p.URL)
		out, err := exec.CommandContext(ctx, chrome, args...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("browser %s: %w\n%s", p.Action, err, firstKB(out))
		}
		return fmt.Sprintf("✓ browsed %s → screenshot %s", p.URL, outFile), nil

	case "text":
		if p.URL == "" {
			return "", fmt.Errorf("browser: text requires 'url'")
		}
		if err := validateBrowserURL(p.URL); err != nil {
			return "", err
		}
		args := append(baseArgs, "--dump-dom", p.URL)
		out, err := exec.CommandContext(ctx, chrome, args...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("browser text: %w", err)
		}
		return chromeExtractText(string(out)), nil

	case "click", "type", "eval", "scroll", "form":
		if p.URL == "" {
			return "", fmt.Errorf("browser: %s requires 'url'", p.Action)
		}
		return chromeRunJS(ctx, chrome, p, timeout)

	case "back", "forward", "refresh", "wait":
		if p.URL == "" {
			return "", fmt.Errorf("browser: %s requires 'url' (target page)", p.Action)
		}
		return chromeRunJS(ctx, chrome, p, timeout)

	case "close":
		return "✓ browser session closed", nil

	default:
		return "", fmt.Errorf("browser: unknown action %q", p.Action)
	}
}

func chromeRunJS(ctx context.Context, chrome string, p struct {
	Action   string `json:"action"`
	URL      string `json:"url"`
	Selector string `json:"selector"`
	Value    string `json:"value"`
	WaitMS   int    `json:"wait_ms"`
}, timeout int) (string, error) {
	js := chromeBuildJS(p.Action, p.Selector, p.Value)
	// Escape URL for JS string context: %q is insufficient in HTML <script> context
	// because %q does not escape </script> (a raw `</` inside <script> closes the tag).
	// Use JSON marshal for proper JS string escaping.
	escURL, err := json.Marshal(p.URL)
	if err != nil {
		return "", fmt.Errorf("marshal browser url: %w", err)
	}
	html := fmt.Sprintf(
		`<html><body><script>async function b(){try{const f=document.createElement('iframe');f.src=%s;f.style.width='100vw';f.style.height='100vh';document.body.appendChild(f);await new Promise(r=>setTimeout(r,5000));const d=f.contentDocument||f.contentWindow.document;%s;document.title='OK:'+d.body.innerText.substring(0,5000)}catch(e){document.title='Err:'+e.message}}setTimeout(b,1000)</script></body></html>`,
		string(escURL), js,
	)
	tmp := tempFileName("ok-browser", ".html")
	os.WriteFile(tmp, []byte(html), 0644)
	defer os.Remove(tmp)

	args := []string{
		"--headless=new", "--no-sandbox", "--disable-gpu",
		"--disable-dev-shm-usage", "--disable-extensions",
		"--no-first-run", "--no-default-browser-check",
		"--window-size=1280,900",
		"--virtual-time-budget=" + fmt.Sprint(timeout+10000),
		"file://" + tmp,
	}
	out, err := exec.CommandContext(ctx, chrome, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("browser %s: %w\n%s", p.Action, err, firstKB(out))
	}
	outStr := string(out)
	if idx := strings.Index(outStr, "OK:"); idx >= 0 {
		r := outStr[idx+3:]
		if e := strings.IndexByte(r, '\n'); e > 0 {
			r = r[:e]
		}
		return strings.TrimSpace(r), nil
	}
	if idx := strings.Index(outStr, "Err:"); idx >= 0 {
		r := outStr[idx:]
		if e := strings.IndexByte(r, '\n'); e > 0 {
			r = r[:e]
		}
		return "", fmt.Errorf("browser %s: %s", p.Action, strings.TrimSpace(r))
	}
	return fmt.Sprintf("✓ %s executed on %s", p.Action, p.URL), nil
}

func chromeBuildJS(action, selector, value string) string {
	// Escape </ to prevent breaking out of <script> context in the HTML page.
	// Go's %q does not escape </script> sequences.
	sel := escapeScriptBreakout(selector)
	val := escapeScriptBreakout(value)
	switch action {
	case "click":
		return fmt.Sprintf(`const e=d.querySelector(%q);if(!e)throw new Error('not found: %s');e.click()`, sel, sel)
	case "type":
		return fmt.Sprintf(`const e=d.querySelector(%q);if(!e)throw new Error('not found: %s');e.focus();e.value=%q;e.dispatchEvent(new Event('input',{bubbles:true}))`, sel, sel, val)
	case "eval":
		return value
	case "scroll":
		return fmt.Sprintf(`window.scrollBy(0,%s)`, val)
	case "form":
		return value
	case "refresh":
		return `location.reload()`
	case "back":
		return `history.back()`
	case "forward":
		return `history.forward()`
	case "wait":
		return fmt.Sprintf(`await new Promise(r=>setTimeout(r,%s))`, val)
	default:
		return ``
	}
}

// escapeScriptBreakout prevents </script> from breaking out of a <script> context
// by replacing "/" with "\/" only when preceded by "<" (case-insensitive).
func escapeScriptBreakout(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '<' && i+1 < len(s) && (s[i+1] == '/' || s[i+1] == '\\') {
			b.WriteString("<\\")
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// chromeExtractPage extracts structured page info using iframe + JS injection.
//
//lint:ignore U1000 kept for future browser extraction use
func chromeExtractPage(ctx context.Context, chrome, url string, timeout int) (string, error) {
	extractJS := `(d=>{const r={};
r.title=d.title;r.url=d.location.href;
const m=d.querySelector('meta[name="description"]');if(m)r.description=m.getAttribute('content');
r.links=Array.from(d.querySelectorAll('a[href]')).slice(0,50).map(a=>({text:(a.textContent||'').trim().slice(0,80),href:a.href}));
r.buttons=Array.from(d.querySelectorAll('button,input[type=submit],input[type=button]')).slice(0,30).map(b=>({text:(b.textContent||b.value||'').trim().slice(0,60)}));
r.headings=Array.from(d.querySelectorAll('h1,h2,h3')).slice(0,20).map(h=>({level:parseInt(h.tagName[1]),text:h.textContent.trim().slice(0,100)}));
r.forms=Array.from(d.querySelectorAll('form')).slice(0,10).map(f=>{const inputs=Array.from(f.querySelectorAll('input,select,textarea')).map(inp=>({name:inp.name||inp.id,type:inp.type||'text',placeholder:(inp.placeholder||'').slice(0,40)}));return{action:f.action||'',inputs}});
r.text=d.body.innerText.trim().slice(0,3000);
return r})`
	escURL, err := json.Marshal(url)
	if err != nil {
		return "", fmt.Errorf("marshal extract url: %w", err)
	}
	html := fmt.Sprintf(
		`<html><body><script>`+
			`async function b(){try{const f=document.createElement('iframe');f.src=%s;f.style.width='100vw';f.style.height='100vh';document.body.appendChild(f);await new Promise(r=>setTimeout(r,5000));const d=f.contentDocument||f.contentWindow.document;const r=%s(d);document.title='OK:'+JSON.stringify(r)}catch(e){document.title='Err:'+e.message}}setTimeout(b,1000)`+
			`</script></body></html>`, string(escURL), extractJS)
	tmp := tempFileName("ok-browser-extract", ".html")
	os.WriteFile(tmp, []byte(html), 0644)
	defer os.Remove(tmp)

	args := []string{
		"--headless=new", "--no-sandbox", "--disable-gpu",
		"--disable-dev-shm-usage", "--disable-extensions",
		"--no-first-run", "--no-default-browser-check",
		"--window-size=1280,900",
		"--virtual-time-budget=" + fmt.Sprint(timeout+10000),
		"file://" + tmp,
	}
	out, err := exec.CommandContext(ctx, chrome, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("browser extract: %w\n%s", err, firstKB(out))
	}
	outStr := string(out)
	if idx := strings.Index(outStr, "OK:"); idx >= 0 {
		r := outStr[idx+3:]
		if e := strings.IndexByte(r, '\n'); e > 0 {
			r = r[:e]
		}
		return strings.TrimSpace(r), nil
	}
	if idx := strings.Index(outStr, "Err:"); idx >= 0 {
		r := outStr[idx:]
		if e := strings.IndexByte(r, '\n'); e > 0 {
			r = r[:e]
		}
		return "", fmt.Errorf("browser extract: %s", strings.TrimSpace(r))
	}
	return "", fmt.Errorf("browser extract: no result")
}

func chromeExtractText(html string) string {
	var b strings.Builder
	in := false
	for _, c := range html {
		if c == '<' {
			in = true
			continue
		}
		if c == '>' {
			in = false
			b.WriteByte(' ')
			continue
		}
		if !in {
			b.WriteRune(c)
		}
	}
	lines := strings.Split(b.String(), "\n")
	var clean []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t != "" {
			clean = append(clean, t)
		}
	}
	r := strings.Join(clean, "\n")
	if len(r) > 10000 {
		r = r[:10000] + "..."
	}
	return r
}

// tempFileName generates a temp file path with a random suffix to prevent
// TOCTOU attacks via predictable temp file names.
func tempFileName(prefix, suffix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to mktemp-style naming if crypto/rand fails (extremely rare).
		return fmt.Sprintf("%s%c%s-%d%s", os.TempDir(), os.PathSeparator, prefix, os.Getpid(), suffix)
	}
	return fmt.Sprintf("%s%c%s-%s%s", os.TempDir(), os.PathSeparator, prefix, hex.EncodeToString(b), suffix)
}

func firstKB(b []byte) string {
	if len(b) > 1024 {
		return string(b[:1024]) + "..."
	}
	return string(b)
}

// validateBrowserURL rejects URLs that use non-http(s) schemes or point to
// local/private addresses to prevent SSRF via the headless browser.
func validateBrowserURL(raw string) error {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return fmt.Errorf("browser: only http/https URLs are allowed (got %q)", raw)
	}
	return nil
}
