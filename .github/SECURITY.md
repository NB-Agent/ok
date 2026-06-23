# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in OK, please **do not** open a public issue. Instead, report it privately:

1. **Email**: security [at] ok.dev (TODO: set up security contact)
2. **GitHub Security Advisory**: Use the "Report a vulnerability" button on the [Security tab](https://github.com/NB-Agent/ok/security)

We aim to acknowledge reports within 48 hours and provide a timeline for a fix within 5 business days.

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 2.x     | :white_check_mark: |
| 1.x     | :x:                |
| 0.x     | :x:                |

## Scope

The following areas are considered in-scope for security reports:

- **Command injection** via tool arguments (bash, edit, grep paths)
- **Path traversal** escaping the workspace or sandbox
- **SSRF** via `web_fetch` reaching internal services
- **Secrets leakage** — API keys, tokens, or credentials in logs, configs, or tool outputs
- **Sandbox escapes** — bypassing Landlock (Linux) or Seatbelt (macOS) confinement
- **Prompt injection** that causes unintended tool execution or data exfiltration
- **Denial of service** — excessive resource consumption via crafted inputs
- **Authentication bypass** in HTTP/SSE or ACP transports

## Out of Scope

- Social engineering attacks against end users
- Physical access attacks
- Vulnerabilities in third-party LLM providers
- Issues that require the user to run untrusted code or plugins they have explicitly trusted

## Design Principles

OK is built with several security-first design choices:

1. **Secrets never touch disk** — API keys are always resolved from environment variables via `api_key_env`, never stored in `.toml` configs
2. **Compile-time sandbox** — `CGO_ENABLED=0` static binaries eliminate dynamic linking surface
3. **Kernel-level confinement** — Landlock (Linux 5.13+) and macOS Seatbelt sandbox tool execution
4. **SSRF protection** — `web_fetch` blocks 13 private IP ranges including cloud metadata endpoints
5. **Path traversal hardening** — `../` sequences and symlink escapes are rejected
6. **No homemade crypto** — only `crypto/sha256` and `crypto/rand` from the standard library
