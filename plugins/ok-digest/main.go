// @ok/digest — MCP plugin: Hashing, encoding, format conversion.
//
// ⚠️  This plugin overlaps with the builtin digest tool at
//
//	internal/tool/builtin/digest.go (which has more actions: url-encode/decode,
//	lowercase/uppercase/trim, bytes hex dump). The builtin is the canonical
//	source. When adding features here, add them to the builtin first.
//	This plugin exists as a standalone MCP server for deployment scenarios
//	where the builtin tool registry is not available.
package main

import (
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
)

func main() {
	s := &mcpServer{name: "ok-digest", version: "1.0.0"}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for dec.More() {
		var req jsonRPC
		if err := dec.Decode(&req); err != nil {
			break
		}
		resp := s.handle(req)
		if resp.ID != nil {
			enc.Encode(resp)
		}
	}
}

type jsonRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpServer struct {
	name    string
	version string
}

func (s *mcpServer) handle(req jsonRPC) jsonRPC {
	id := req.ID
	switch req.Method {
	case "initialize":
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})}
	case "tools/list":
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"tools": []map[string]any{
				{"name": "digest_hash", "description": "Hash text with MD5/SHA1/SHA256/SHA512",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"text":      map[string]any{"type": "string"},
							"algorithm": map[string]any{"type": "string", "enum": []string{"md5", "sha1", "sha256", "sha512"}},
							"path":      map[string]any{"type": "string"}},
						"required": []string{"algorithm"}}},
				{"name": "digest_base64", "description": "Base64 encode or decode",
					"inputSchema": map[string]any{
						"type": "object", "properties": map[string]any{
							"action": map[string]any{"type": "string", "enum": []string{"encode", "decode"}},
							"text":   map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}},
						"required": []string{"action"}}},
				{"name": "digest_hex", "description": "Hex encode or decode",
					"inputSchema": map[string]any{
						"type": "object", "properties": map[string]any{
							"action": map[string]any{"type": "string", "enum": []string{"encode", "decode"}},
							"text":   map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}},
						"required": []string{"action"}}},
				{"name": "digest_count", "description": "Count words/lines/bytes in text",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
						"text": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}}},
				{"name": "digest_json", "description": "Format or validate JSON",
					"inputSchema": map[string]any{
						"type": "object", "properties": map[string]any{
							"action": map[string]any{"type": "string", "enum": []string{"format", "validate"}},
							"text":   map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}},
						"required": []string{"action"}}},
			},
		})}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)
		result, err := s.execute(params.Name, params.Arguments)
		if err != nil {
			return jsonRPC{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32000, Message: err.Error()}}
		}
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"content": []map[string]any{{"type": "text", "text": result}},
		})}
	default:
		return jsonRPC{JSONRPC: "2.0", ID: id}
	}
}

func (s *mcpServer) execute(name string, args json.RawMessage) (string, error) {
	var p struct {
		Text      string `json:"text"`
		Algorithm string `json:"algorithm"`
		Action    string `json:"action"`
		Path      string `json:"path"`
	}
	json.Unmarshal(args, &p)
	data := []byte(p.Text)
	if p.Path != "" {
		var err error
		data, err = os.ReadFile(p.Path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", p.Path, err)
		}
	}
	switch name {
	case "digest_hash":
		return hashText(data, p.Algorithm)
	case "digest_base64":
		return b64Op(data, p.Action)
	case "digest_hex":
		return hexOp(data, p.Action)
	case "digest_count":
		return countOp(data)
	case "digest_json":
		return jsonOp(data, p.Action)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func hashText(data []byte, algo string) (string, error) {
	var h hash.Hash
	switch strings.ToLower(algo) {
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
		return "", fmt.Errorf("unsupported algorithm: %s", algo)
	}
	h.Write(data)
	result := fmt.Sprintf("%s: %x", algo, h.Sum(nil))
	switch strings.ToLower(algo) {
	case "md5":
		result += "\n\n⚠️  WARNING: MD5 is cryptographically broken and should NOT be used for security purposes (collision attacks are practical). Use SHA256 instead."
	case "sha1":
		result += "\n\n⚠️  WARNING: SHA1 is cryptographically broken and should NOT be used for security purposes (SHAttered attack, 2017). Use SHA256 instead."
	}
	return result, nil
}

func b64Op(data []byte, action string) (string, error) {
	switch action {
	case "encode":
		return base64.StdEncoding.EncodeToString(data), nil
	case "decode":
		d, err := base64.StdEncoding.DecodeString(string(data))
		if err != nil {
			return "", fmt.Errorf("base64 decode: %w", err)
		}
		return string(d), nil
	}
	return "", fmt.Errorf("unsupported base64 action: %s", action)
}

func hexOp(data []byte, action string) (string, error) {
	switch action {
	case "encode":
		return hex.EncodeToString(data), nil
	case "decode":
		d, err := hex.DecodeString(string(data))
		if err != nil {
			return "", fmt.Errorf("hex decode: %w", err)
		}
		return string(d), nil
	}
	return "", fmt.Errorf("unsupported hex action: %s", action)
}

func countOp(data []byte) (string, error) {
	s := string(data)
	lines := 0
	for _, c := range s {
		if c == '\n' {
			lines++
		}
	}
	words := len(strings.Fields(s))
	return fmt.Sprintf("lines: %d\nwords: %d\nbytes: %d", lines, words, len(data)), nil
}

func jsonOp(data []byte, action string) (string, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		if action == "validate" {
			return fmt.Sprintf("invalid JSON: %s", err), nil
		}
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	if action == "format" {
		out, _ := json.MarshalIndent(v, "", "  ")
		return string(out), nil
	}
	return "valid JSON", nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
