# OK v4 Plugin Development Guide

> Write one MCP plugin, deploy to any registry, serve any user.
> Any language. Any platform. Any registry.

---

## Quick start (5 minutes)

Create a new plugin in any language:

```
my-plugin/
├── main.go          # MCP server (any language)
├── plugin.json      # metadata
├── README.md
└── Makefile
```

### Step 1: Implement the MCP protocol

Your plugin speaks **JSON-RPC 2.0** over **stdin/stdout**.

```json
// → Initialize (handshake)
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}
// ← Response
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"my-plugin","version":"1.0.0"},"capabilities":{"tools":{}}}}

// → List tools
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
// ← Response
{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"my_tool","description":"Does something","inputSchema":{"type":"object","properties":{"param":{"type":"string"}}}}]}}

// → Call tool
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"my_tool","arguments":{"param":"value"}}}
// ← Response
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"result"}]}}
```

### Step 2: Create plugin.json

```json
{
  "name": "@yourname/my-plugin",
  "version": "1.0.0",
  "description": "What your plugin does",
  "author": "Your Name",
  "license": "MIT",
  "tags": ["database", "postgres"],
  "min_ok_version": "4.0.0",
  "tools": ["my_tool"],
  "entrypoint": "my-plugin",
  "transport": "stdio"
}
```

### Step 3: Test locally

```toml
# ok.toml
[[plugins]]
name = "my-plugin"
type = "stdio"
command = "./my-plugin/my-plugin"
```

```bash
ok run "use my_tool to do something"
```

### Step 4: Publish

Upload to any registry:

```bash
# Official registry
curl -X POST https://plugins.ok.sh/api/v1/plugins \
  -H "Authorization: Bearer $OK_REGISTRY_TOKEN" \
  -F "plugin=@plugin.json" \
  -F "archive=@my-plugin.tar.gz"

# Or your own registry
scp my-plugin.tar.gz user@server:/var/www/plugins/
```

---

## Plugin specs

### Sandbox isolation

All plugins run isolated. Default policy:

```
Network:   false (unless declared)
Filesystem: read workspace only
Process:   no child processes
```

Override in plugin.json:

```json
{
  "capabilities": {
    "network": true,
    "filesystem": {"read": ["/workspace"], "write": []},
    "processes": false
  }
}
```

### Versioning

```
@ok/git@1.0.0    Specific version
@ok/git@^1.0     Compatible (>=1.0.0, <2.0.0)
@ok/git@latest   Latest stable
```

### Lock file

```toml
[plugins.lock]
"@ok/git" = "1.2.0"
"@my-company/db" = "0.5.0"

[plugins.overrides]
"@ok/deploy" = "file:///opt/company/deploy-mcp"
```

---

## Official plugins (reference)

| Plugin | Tools | Language | Lines |
|--------|-------|----------|:-----:|
| `@ok/git` | status, diff, log, commit, branch | Go | 149 |
| `@ok/database-sqlite` | query, exec | Go | ~200 |
| `@ok/web-fetch` | fetch | Go | ~150 |
| `@ok/jobs` | run, status, kill | Go | ~180 |

---

## Plugin best practices

1. **One binary, no dependencies** — the user shouldn't need `npm install` or `pip install`
2. **Tool names are kebab-case** — `git_status` not `GitStatus`
3. **Every tool has a description** — LLM reads it to decide when to use your plugin
4. **Input schemas are precise** — mark required fields, set defaults
5. **Error messages are actionable** — "file not found: main.go" not "error occurred"
6. **Keep state in memory** — don't assume persistence between calls
7. **Test with `ok run "use <your-plugin>"`** before publishing
