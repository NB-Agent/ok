# OK - AI Coding Agent for VS Code

OK is an AI coding agent with an OS-level sandbox and task sub-agents. This extension provides a VS Code frontend for the OK CLI.

![screenshot](screenshot.png)

## Prerequisites

You must have the **OK CLI** binary installed and available on your system `PATH`, or explicitly configured in the extension settings.

- Install the `ok` CLI by following the instructions in the [main OK repository](https://github.com/ok-ai/ok).
- Verify the installation by running `ok --version` in your terminal.

## Features

- **Start / Stop Sessions** — Launch and manage OK CLI sessions from the VS Code sidebar.
- **Run Tasks** — Submit natural-language tasks to the OK agent via the command palette or the sidebar input box.
- **View Agent Status** — See the current agent state (idle / running / error) at a glance.
- **Audit Logs** — Watch real-time task output and agent responses in the sidebar webview.

## Configuration

| Setting | Description |
|---------|-------------|
| `ok.binaryPath` | Path to the `ok` CLI binary. Leave empty to search the system `PATH`. |
| `ok.defaultModel` | Default model to use for the agent. Leave empty to use the CLI config default. |

Open VS Code **Settings** (`Ctrl+,` / `Cmd+,`) and search for `ok` to configure these options.

## Commands

| Command | Title | Description |
|---------|-------|-------------|
| `ok.startSession` | OK: Start New Session | Reveal the sidebar and start a fresh session. |
| `ok.submitTask` | OK: Run Task | Open an input box to describe a task for the agent. |

## Extension Settings

This extension contributes the following settings:

- `ok.binaryPath`: Path to the `ok` CLI binary. Empty = search PATH.
- `ok.defaultModel`: Default model to use. Empty = use config default.

## Contributing

1. Clone the repository:
   ```bash
   git clone https://github.com/ok-ai/ok.git
   cd ok/editors/vscode
   ```
2. Install dependencies:
   ```bash
   npm install
   ```
3. Compile the TypeScript:
   ```bash
   npm run compile
   ```
4. Press `F5` in VS Code to launch the Extension Development Host and test the extension.

### Pull Requests

- Ensure the TypeScript compiles without errors (`npm run compile`).
- Update the `CHANGELOG.md` if your change is user-facing.
- Follow the existing code style (2-space indentation, semicolons, etc.).

## License

MIT
