package builtin

// ⚠️  This builtin digest has a sibling MCP plugin at plugins/ok-digest/
//    with overlapping functionality (hash/base64/hex/count/json).
//    THIS FILE is the canonical implementation — add new features here first.
//    When updating logic here, check if the plugin needs the same change.

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"os"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(digestTool{}) }

type digestTool struct{}

func (digestTool) Name() string { return "digest" }

func (digestTool) Description() string {
	return "Compute hashes, encode/decode, format JSON/YAML/TOML, and convert between data formats. Supports MD5, SHA1, SHA256, SHA512, base64, hex."
}

func (digestTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"action":{"enum":["hash","base64-encode","base64-decode","hex-encode","hex-decode","url-encode","url-decode","json-format","json-validate","json-to-yaml","lowercase","uppercase","trim","count","bytes"],"type":"string"},"algorithm":{"enum":["md5","sha1","sha256","sha512"],"type":"string"},"path":{"type":"string"},"text":{"type":"string"}},"required":["action"],"type":"object"}`)
}

func (digestTool) ReadOnly() bool { return true }

func (digestTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action    string `json:"action"`
		Text      string `json:"text"`
		Path      string `json:"path"`
		Algorithm string `json:"algorithm"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	// Read input: path takes priority over text.
	var input []byte
	if p.Path != "" {
		var err error
		input, err = os.ReadFile(p.Path)
		if err != nil {
			return "", fmt.Errorf("read file: %w", err)
		}
	} else {
		input = []byte(p.Text)
	}

	inputStr := string(input)

	switch p.Action {
	case "hash":
		algo := p.Algorithm
		if algo == "" {
			algo = "sha256"
		}
		var h hash.Hash
		switch algo {
		case "md5":
			// MD5/SHA1 are supported for backward-compatible hash computation (checksums, data integrity).
			// This is NOT a security-sensitive use — users explicitly request these algorithms.
			h = md5.New()
		case "sha1":
			// MD5/SHA1 are supported for backward-compatible hash computation (checksums, data integrity).
			// This is NOT a security-sensitive use — users explicitly request these algorithms.
			h = sha1.New()
		case "sha256":
			h = sha256.New()
		case "sha512":
			h = sha512.New()
		default:
			return "", fmt.Errorf("unknown algorithm: %s", algo)
		}
		h.Write(input)
		sum := hex.EncodeToString(h.Sum(nil))
		return fmt.Sprintf("# Hash (%s)\n\n`%s`\n", algo, sum), nil

	case "base64-encode":
		return fmt.Sprintf("# Base64\n\n`%s`\n", base64.StdEncoding.EncodeToString(input)), nil

	case "base64-decode":
		decoded, err := base64.StdEncoding.DecodeString(inputStr)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(inputStr)
			if err != nil {
				return "", fmt.Errorf("base64 decode: %w", err)
			}
		}
		return fmt.Sprintf("# Base64 Decode\n\n%s\n", string(decoded)), nil

	case "hex-encode":
		return fmt.Sprintf("# Hex\n\n`%s`\n", hex.EncodeToString(input)), nil

	case "hex-decode":
		decoded, err := hex.DecodeString(inputStr)
		if err != nil {
			return "", fmt.Errorf("hex decode: %w", err)
		}
		return fmt.Sprintf("# Hex Decode\n\n%s\n", string(decoded)), nil

	case "url-encode":
		return fmt.Sprintf("# URL Encode\n\n`%s`\n", strings.ReplaceAll(inputStr, " ", "%20")), nil

	case "url-decode":
		s := strings.ReplaceAll(inputStr, "%20", " ")
		return fmt.Sprintf("# URL Decode\n\n%s\n", s), nil

	case "json-format":
		var v any
		if err := json.Unmarshal(input, &v); err != nil {
			return "", fmt.Errorf("invalid JSON: %w", err)
		}
		formatted, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal formatted JSON: %w", err)
		}
		return fmt.Sprintf("# JSON Formatted\n\n```json\n%s\n```\n", string(formatted)), nil

	case "json-validate":
		var v any
		if err := json.Unmarshal(input, &v); err != nil {
			return fmt.Sprintf("# JSON Validate\n\n❌ Invalid: %v\n", err), nil
		}
		return "# JSON Validate\n\n✅ Valid JSON\n", nil

	case "lowercase":
		return fmt.Sprintf("# Lowercase\n\n%s\n", strings.ToLower(inputStr)), nil

	case "uppercase":
		return fmt.Sprintf("# Uppercase\n\n%s\n", strings.ToUpper(inputStr)), nil

	case "trim":
		return fmt.Sprintf("# Trim\n\n%s\n", strings.TrimSpace(inputStr)), nil

	case "count":
		lines := strings.Split(inputStr, "\n")
		chars := len(inputStr)
		words := len(strings.Fields(inputStr))
		return fmt.Sprintf("# Count\n\n- Characters: %d\n- Words: %d\n- Lines: %d\n", chars, words, len(lines)), nil

	case "bytes":
		var b bytes.Buffer
		// Print hex dump: 16 bytes per line
		data := input
		for i := 0; i < len(data); i += 16 {
			end := i + 16
			if end > len(data) {
				end = len(data)
			}
			fmt.Fprintf(&b, "%08x  ", i)
			for j := i; j < end; j++ {
				fmt.Fprintf(&b, "%02x ", data[j])
			}
			for j := end; j < i+16; j++ {
				b.WriteString("   ")
			}
			b.WriteString(" ")
			for j := i; j < end; j++ {
				if data[j] >= 32 && data[j] <= 126 {
					b.WriteByte(data[j])
				} else {
					b.WriteByte('.')
				}
			}
			b.WriteByte('\n')
		}
		return fmt.Sprintf("# Bytes (%d)\n\n```\n%s```\n", len(data), b.String()), nil

	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}
