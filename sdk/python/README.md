# OK Agent Python SDK

Connect to a running [OK Agent](https://github.com/ok/ok) instance via HTTP/SSE. Send prompts, stream typed events, and integrate OK's capabilities into your Python applications — data science notebooks, ML pipelines, automation scripts, and more.

## Installation

```bash
pip install ok-sdk
```

Or install from source:

```bash
cd sdk/python
pip install -e .
```

## Quick Start

```python
from ok import Agent

# Connect to a local OK agent (default: http://127.0.0.1:3030)
agent = Agent()

if not agent.connect():
    print("Start OK first:  ok serve")
    exit(1)

# Simple query — get a result object
result = agent.run("What is the capital of France?")
print(result.text)
# → "The capital of France is Paris."

# With auto-approve for tool calls
result = agent.run("List the files in my home directory", auto_approve=True)
print(result.text)
print(f"Tokens used: {result.usage['total_tokens']}")
```

## Streaming Events

For fine-grained control, stream events individually:

```python
from ok import Agent, TEXT, USAGE, TOOL_DISPATCH, TOOL_RESULT, DONE

agent = Agent()

# Submit text (non-blocking)
agent.submit("Analyze this Python script and suggest improvements")

# Stream events
for event in agent.stream(auto_approve=True):
    if event.kind == TEXT:
        print(event.text, end="", flush=True)
    elif event.kind == REASONING:
        print(f"\n[thinking: {event.reasoning[:60]}...]")
    elif event.kind == TOOL_DISPATCH:
        print(f"\n🔧 {event.tool.name}({event.tool.args[:80]})")
    elif event.kind == TOOL_RESULT:
        print(f"  → {event.tool.output[:80]}...")
    elif event.kind == USAGE:
        print(f"\n[Tokens: {event.usage.total_tokens} | ${event.usage.cost_usd:.4f}]")
    elif event.kind == DONE:
        print("\n✅ Done")
        break
```

## Handling Approvals

```python
agent = Agent()

result, pending = agent.ask_question("Delete all files in /tmp")
if pending:
    for event in pending:
        if event.approval:
            print(f"Approve {event.approval.tool}: {event.approval.subject}?")
            # agent.approve(event.approval.id, allow=True)
```

## Session Management

```python
agent = Agent()

# Check session context usage
ctx = agent.get_context()
print(f"Using {ctx['used']} / {ctx['window']} tokens")

# Get conversation history
for msg in agent.get_history():
    print(f"{msg['role']}: {msg['content'][:100]}")

# Start a fresh session
agent.new_session()

# Compact to save tokens
agent.compact()

# Clean up
agent.close()
```

## Jupyter Notebook Integration

```python
# In a Jupyter notebook
from ok import Agent

agent = Agent()

# Run analysis and display results
result = agent.run("""
Analyze the dataset and explain:
1. Key statistical properties
2. Outliers and anomalies
3. Recommended visualizations
""", auto_approve=True)

print(result.text)
```

## API Reference

### `Agent(config=None, transport=None)`

Main client class.

- `connect()` → `bool` — check if agent is reachable
- `run(text, auto_approve=None, timeout_sec=None)` → `RunResult` — submit and wait
- `submit(text)` — non-blocking submit
- `stream(auto_approve=False)` → generator of `Event`
- `cancel()` — cancel current turn
- `new_session()` — start fresh conversation
- `get_history()` → `list[dict]` — conversation history
- `get_context()` → `dict` — token usage stats
- `compact()` — compact session
- `close()` — close HTTP connection

### `RunResult`

Aggregated result from `agent.run()`.

- `text` — all text output concatenated
- `tool_calls` — list of tool invocations
- `usage` — token usage statistics
- `cancelled` — was the turn cancelled
- `error` — error message if any

### `Event`

Typed event from the agent's SSE stream.

- `kind` — one of `TEXT`, `REASONING`, `MESSAGE`, `TOOL_DISPATCH`, `TOOL_RESULT`, `USAGE`, `APPROVAL`, `ASK`, `DONE`, `CANCELLED`, `ERROR`
- `text` — text content (for TEXT/MESSAGE events)
- `tool` — `ToolCall` (for TOOL_DISPATCH/TOOL_RESULT)
- `usage` — `Usage` (for USAGE events)
- `approval` — `ApprovalRequest` (for APPROVAL events)
- `ask` — `AskState` (for ASK events)

## Development

```bash
cd sdk/python
pip install -e ".[dev]"
pytest
```

## License

Apache 2.0
