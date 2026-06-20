package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type vulnCheckAnalyzer struct{}

func (vulnCheckAnalyzer) Name() string        { return "vuln-check" }
func (vulnCheckAnalyzer) Layer() string       { return "external" }
func (vulnCheckAnalyzer) Languages() []string { return []string{"*"} }

func (a vulnCheckAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	var ff []Finding
	langs := DetectLanguages(root)
	ff = append(ff, a.runGovulncheck(ctx, root, langs)...)
	ff = append(ff, a.runNpmAudit(ctx, root, langs)...)
	ff = append(ff, a.runPipAudit(ctx, root, langs)...)
	ff = append(ff, a.runCargoAudit(ctx, root, langs)...)
	ff = append(ff, a.runTrivy(ctx, root, langs)...)
	ff = append(ff, a.checkLockFreshness(ctx, root, langs)...)
	ff = append(ff, a.checkUnpinnedDeps(ctx, root, langs)...)
	return ff, nil
}

func (a vulnCheckAnalyzer) runGovulncheck(ctx context.Context, root string, langs []string) []Finding {
	if !contains(langs, "go") || !which("govulncheck") {
		return nil
	}
	out, err := runCmd(ctx, root, "govulncheck", "-json", "./...")
	if err != nil {
		_ = err
	}
	return parseGovulncheckJSON(out)
}

func (a vulnCheckAnalyzer) runNpmAudit(ctx context.Context, root string, langs []string) []Finding {
	if !containsAny(langs, []string{"javascript", "typescript"}) || !which("npm") {
		return nil
	}
	out, err := runCmd(ctx, root, "npm", "audit", "--json")
	if err != nil {
		_ = err
	}
	return parseNpmAudit(out)
}

func (a vulnCheckAnalyzer) runPipAudit(ctx context.Context, root string, langs []string) []Finding {
	if !contains(langs, "python") {
		return nil
	}
	if which("pip-audit") {
		out, err := runCmd(ctx, root, "pip-audit", "--json", "--requirement", "requirements.txt")
		if err != nil {
			_ = err
		}
		return parsePipAudit(out)
	}
	if which("safety") {
		out, err := runCmd(ctx, root, "safety", "check", "--json")
		if err != nil {
			_ = err
		}
		return parseSafety(out)
	}
	return nil
}

func (a vulnCheckAnalyzer) runCargoAudit(ctx context.Context, root string, langs []string) []Finding {
	if !contains(langs, "rust") || !which("cargo") {
		return nil
	}
	out, err := runCmd(ctx, root, "cargo", "audit", "--json")
	if err != nil {
		_ = err
	}
	return parseCargoAudit(out)
}

func (a vulnCheckAnalyzer) runTrivy(ctx context.Context, root string, _ []string) []Finding {
	if !which("trivy") {
		return nil
	}
	out, err := runCmd(ctx, root, "trivy", "fs", "--format=json", "--quiet", ".")
	if err != nil {
		_ = err
	}
	return parseTrivy(out)
}

func (a vulnCheckAnalyzer) checkLockFreshness(_ context.Context, root string, langs []string) []Finding {
	// Simplified: just check go.sum exists for Go projects
	if contains(langs, "go") && !fileExists(root+"/go.sum") {
		return []Finding{{Analyzer: "vuln-check", Layer: "semantic", Severity: SevMedium,
			File: "go.sum", Line: 1, Message: "missing go.sum — run go mod tidy",
			Category: "security", Rule: "VULN-S001", Fix: "run 'go mod tidy'"}}
	}
	return nil
}

var noPinRe = regexp.MustCompile(`\s*([a-zA-Z][a-zA-Z0-9_.-]*)\s*>=`)

func (a vulnCheckAnalyzer) checkUnpinnedDeps(_ context.Context, root string, langs []string) []Finding {
	if !contains(langs, "python") {
		return nil
	}
	if !fileExists(root + "/requirements.txt") {
		return nil
	}
	data, err := readFile(root + "/requirements.txt")
	if err != nil {
		return nil
	}
	var ff []Finding
	for _, line := range strings.Split(string(data), "\n") {
		m := noPinRe.FindStringSubmatch(line)
		if m != nil {
			ff = append(ff, Finding{
				Analyzer: "vuln-check", Layer: "semantic", Severity: SevLow,
				File: "requirements.txt", Line: 1,
				Message:  fmt.Sprintf("unpinned dependency %s — pin exact version", m[1]),
				Category: "security", Rule: "VULN-S002",
			})
		}
	}
	return ff
}

// ─── Parsers ──────────────────────────────────────────────────────────────

func parseGovulncheckJSON(output string) []Finding {
	var ff []Finding
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		type vulnEvent struct {
			Type   string `json:"@type"`
			Vuln   string `json:"vuln,omitempty"`
			Module string `json:"module,omitempty"`
			OSVs   []struct {
				ID      string `json:"id"`
				FixedIn string `json:"fixed"`
			} `json:"osvs,omitempty"`
		}
		var ev vulnEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Type != "vuln" {
			continue
		}
		f := Finding{Analyzer: "vuln-check", Layer: "external", Severity: SevHigh,
			File: "go.mod", Line: 1,
			Message:  fmt.Sprintf("Go vulnerability %s in %s", ev.Vuln, ev.Module),
			Category: "security", Rule: "VULN-G001"}
		if len(ev.OSVs) > 0 {
			f.Rule = ev.OSVs[0].ID
			if ev.OSVs[0].FixedIn != "" {
				f.Fix = fmt.Sprintf("upgrade %s to %s", ev.Module, ev.OSVs[0].FixedIn)
			}
		}
		ff = append(ff, f)
	}
	return ff
}

func parseNpmAudit(output string) []Finding {
	type npmResult struct {
		Vulnerabilities map[string]struct {
			Severity     string `json:"severity"`
			FixAvailable string `json:"fixAvailable"`
		} `json:"vulnerabilities"`
	}
	var data npmResult
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return nil
	}
	var ff []Finding
	for pkg, v := range data.Vulnerabilities {
		sev := SevMedium
		switch v.Severity {
		case "critical":
			sev = SevCritical
		case "high":
			sev = SevHigh
		}
		f := Finding{Analyzer: "vuln-check", Layer: "external", Severity: sev,
			File: "package.json", Line: 1,
			Message:  fmt.Sprintf("npm: %s (%s severity)", pkg, v.Severity),
			Category: "security", Rule: "VULN-J001"}
		if strings.Contains(v.FixAvailable, "npm audit fix") {
			f.Fix = "run npm audit fix"
		}
		ff = append(ff, f)
	}
	return ff
}

func parsePipAudit(output string) []Finding {
	type pipResult struct {
		Dependencies []struct {
			Name  string `json:"name"`
			Vulns []struct {
				ID      string `json:"id"`
				Details string `json:"description"`
			} `json:"vulns"`
		} `json:"dependencies"`
	}
	var data pipResult
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return nil
	}
	var ff []Finding
	for _, dep := range data.Dependencies {
		for _, v := range dep.Vulns {
			ff = append(ff, Finding{
				Analyzer: "vuln-check", Layer: "external", Severity: SevHigh,
				File: "requirements.txt", Line: 1,
				Message:  fmt.Sprintf("pip: %s — %s (%s)", dep.Name, v.Details, v.ID),
				Category: "security", Rule: v.ID,
			})
		}
	}
	return ff
}

func parseSafety(output string) []Finding {
	type safetyResult []struct {
		Name string `json:"name"`
		Vuln string `json:"vulnerability"`
		ID   string `json:"id"`
	}
	var data safetyResult
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return nil
	}
	var ff []Finding
	for _, v := range data {
		ff = append(ff, Finding{
			Analyzer: "vuln-check", Layer: "external", Severity: SevHigh,
			File: "requirements.txt", Line: 1,
			Message:  fmt.Sprintf("safety: %s — %s (%s)", v.Name, v.Vuln, v.ID),
			Category: "security", Rule: v.ID,
		})
	}
	return ff
}

func parseCargoAudit(output string) []Finding {
	type cargoResult struct {
		Vulnerabilities struct {
			List []struct {
				Package struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"package"`
				Advisory struct {
					ID       string `json:"id"`
					Title    string `json:"title"`
					Severity string `json:"severity"`
				} `json:"advisory"`
			} `json:"list"`
		} `json:"vulnerabilities"`
	}
	var data cargoResult
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return nil
	}
	var ff []Finding
	for _, v := range data.Vulnerabilities.List {
		sev := SevMedium
		switch v.Advisory.Severity {
		case "critical":
			sev = SevCritical
		case "high":
			sev = SevHigh
		}
		ff = append(ff, Finding{
			Analyzer: "vuln-check", Layer: "external", Severity: sev,
			File: "Cargo.lock", Line: 1,
			Message:  fmt.Sprintf("cargo: %s@%s — %s (%s)", v.Package.Name, v.Package.Version, v.Advisory.Title, v.Advisory.ID),
			Category: "security", Rule: v.Advisory.ID,
		})
	}
	return ff
}

func parseTrivy(output string) []Finding {
	type trivyResult struct {
		Results []struct {
			Target          string `json:"target"`
			Vulnerabilities []struct {
				ID           string `json:"vulnerabilityID"`
				PkgName      string `json:"pkgName"`
				Severity     string `json:"severity"`
				Title        string `json:"title"`
				FixedVersion string `json:"fixedVersion"`
			} `json:"vulnerabilities"`
		} `json:"Results"`
	}
	var data trivyResult
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return nil
	}
	var ff []Finding
	for _, res := range data.Results {
		for _, v := range res.Vulnerabilities {
			sev := SevMedium
			switch v.Severity {
			case "CRITICAL":
				sev = SevCritical
			case "HIGH":
				sev = SevHigh
			}
			f := Finding{
				Analyzer: "vuln-check", Layer: "external", Severity: sev,
				File: res.Target, Line: 1,
				Message:  fmt.Sprintf("trivy: %s — %s (%s)", v.PkgName, v.Title, v.ID),
				Category: "security", Rule: v.ID,
			}
			if v.FixedVersion != "" {
				f.Fix = fmt.Sprintf("upgrade %s to %s", v.PkgName, v.FixedVersion)
			}
			ff = append(ff, f)
		}
	}
	return ff
}
