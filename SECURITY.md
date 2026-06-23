# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in OK, please **do not open a public
issue**. Instead, email the maintainers with details:

- **GitHub**: Open a [Security Advisory](https://github.com/NB-Agent/ok/security/advisories/new) or email [security@nbyyds.com](mailto:security@nbyyds.com)

We will acknowledge your report within 48 hours and provide an estimated
timeline for a fix. We appreciate responsible disclosure and will credit
researchers who report valid issues.

## Supported Versions

| Version | Supported          |
|---------|--------------------|
| 1.x     | :white_check_mark: |
| 0.x     | :x:                |

Only the latest release receives security patches. Older major versions are
supported for 6 months after a new major version ships.

## What to Report

Issues that could compromise the safety of an OK session are in scope:

- **Tool sandbox escapes** — paths that allow `write_file`/`edit_file`/`bash`
  to access files outside the configured workspace.
- **Plugin transport vulnerabilities** — command injection in stdio transports,
  SSRF in HTTP/SSE transports.
- **Prompt injection vectors** — ways an untrusted input (MCP resource, file
  content, shell output) can escape context boundaries and alter agent behavior.
- **Secrets exposure** — API keys or configuration secrets accidentally logged,
  emitted to event streams, or persisted to disk.

## Out of Scope

- `bash` tool is designed to execute arbitrary shell commands — it is not a
  vulnerability if a model generates a destructive command. The *sandbox*
  (Windows AppContainer, Linux Landlock, macOS Seatbelt) is what limits blast
  radius. If the sandbox allows a command to escape its confinement, that IS in
  scope.

## Acknowledgments

We maintain a list of security researchers who have responsibly disclosed
vulnerabilities. Thank you for helping keep OK safe for everyone.
