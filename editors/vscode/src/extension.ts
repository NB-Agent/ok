import * as vscode from 'vscode';
import { OkWebviewViewProvider } from './webviewProvider';
import * as cp from 'child_process';
import * as http from 'http';
import * as https from 'https';
import * as fs from 'fs';
import * as path from 'path';

// GitHub repo for binary downloads
const GITHUB_REPO = 'ok-ai/ok';

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

let outputChannel: vscode.OutputChannel;
let okProcess: cp.ChildProcess | undefined;
let webviewProvider: OkWebviewViewProvider | undefined;
let okPort = 8787;
let resolvedBinaryPath: string | undefined;

// ---------------------------------------------------------------------------
// OK Server — manage ok serve lifecycle
// ---------------------------------------------------------------------------

function okPath(): string {
    if (resolvedBinaryPath) return resolvedBinaryPath;
    const cfg = vscode.workspace.getConfiguration('ok');
    return cfg.get<string>('binaryPath', 'ok');
}

async function startOkServer(): Promise<number> {
    const binary = okPath();
    outputChannel.appendLine(`[OK] Starting: ${binary} serve --addr 127.0.0.1:${okPort}`);

    okProcess = cp.spawn(binary, ['serve', '--addr', `127.0.0.1:${okPort}`], {
        cwd: vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? process.cwd(),
        stdio: ['ignore', 'pipe', 'pipe'],
        env: { ...process.env },
    });

    okProcess.stdout?.on('data', (data: Buffer) => {
        outputChannel.append(`[OK stdout] ${data.toString()}`);
    });

    okProcess.stderr?.on('data', (data: Buffer) => {
        outputChannel.append(`[OK stderr] ${data.toString()}`);
    });

    okProcess.on('exit', (code, signal) => {
        outputChannel.appendLine(`[OK] Server exited (code=${code}, signal=${signal})`);
        okProcess = undefined;
    });

    okProcess.on('error', (err) => {
        outputChannel.appendLine(`[OK] Server error: ${err.message}`);
        okProcess = undefined;
    });

    // Wait for server to be ready
    for (let i = 0; i < 30; i++) {
        try {
            await healthCheck(okPort);
            outputChannel.appendLine(`[OK] Server ready on port ${okPort}`);
            return okPort;
        } catch {
            await sleep(500);
        }
    }

    throw new Error(`OK server did not start within 15s on port ${okPort}`);
}

function healthCheck(port: number): Promise<void> {
    return new Promise((resolve, reject) => {
        const req = http.get(`http://127.0.0.1:${port}/healthz`, (res) => {
            if (res.statusCode === 200) resolve();
            else reject(new Error(`healthz returned ${res.statusCode}`));
        });
        req.on('error', reject);
        req.setTimeout(2000, () => { req.destroy(); reject(new Error('timeout')); });
    });
}

function sleep(ms: number): Promise<void> {
    return new Promise(resolve => setTimeout(resolve, ms));
}

// ---------------------------------------------------------------------------
// Binary download — auto-download ok CLI from GitHub Releases
// ---------------------------------------------------------------------------

/** Map Node.js platform/arch to GitHub Release asset name */
function platformAssetName(): string | null {
    const platMap: Record<string, string> = {
        win32: 'windows',
        darwin: 'darwin',
        linux: 'linux',
    };
    const archMap: Record<string, string> = {
        x64: 'amd64',
        arm64: 'arm64',
    };
    const goos = platMap[process.platform];
    const goarch = archMap[process.arch];
    if (!goos || !goarch) return null;
    const ext = process.platform === 'win32' ? '.exe' : '';
    return `ok-${goos}-${goarch}${ext}`;
}

/** Download a file from a URL to a local path (handles GitHub redirects) */
async function downloadFile(url: string, destPath: string): Promise<void> {
    return new Promise((resolve, reject) => {
        const file = fs.createWriteStream(destPath);
        const doGet = (u: string) => {
            https.get(u, (res) => {
                const sc = res.statusCode ?? 0;
                if (sc >= 300 && sc < 400 && res.headers.location) {
                    file.close();
                    fs.unlinkSync(destPath);
                    doGet(res.headers.location);
                    return;
                }
                if (sc !== 200) {
                    file.close();
                    fs.unlinkSync(destPath);
                    reject(new Error(`Download failed: HTTP ${sc}`));
                    return;
                }
                res.pipe(file);
                file.on('finish', () => { file.close(); resolve(); });
            }).on('error', (err) => {
                file.close();
                try { fs.unlinkSync(destPath); } catch { /* ignore */ }
                reject(err);
            });
        };
        doGet(url);
    });
}

/**
 * Ensure the ok binary is available. Resolution chain:
 * 1. User-configured `ok.binaryPath` (if non-default) — check existence
 * 2. System PATH — `which ok` / `where ok`
 * 3. Cached download under `globalStorageUri`
 * 4. Download from GitHub Releases latest → cache
 */
async function ensureBinary(context: vscode.ExtensionContext): Promise<string> {
    // Step 1: user-configured custom path
    const cfg = vscode.workspace.getConfiguration('ok');
    const configured = cfg.get<string>('binaryPath', 'ok');
    if (configured !== 'ok') {
        try {
            await fs.promises.access(configured, fs.constants.R_OK);
            outputChannel.appendLine(`[OK] Using configured binary: ${configured}`);
            return configured;
        } catch {
            outputChannel.appendLine(`[OK] Configured binary not found at "${configured}", falling back`);
        }
    }

    // Step 2: system PATH
    try {
        const whichCmd = process.platform === 'win32' ? 'where ok' : 'which ok';
        const result = await new Promise<string>((resolve, reject) => {
            cp.exec(whichCmd, (err, stdout) => {
                if (err) reject(err);
                else resolve(stdout.trim().split('\n')[0]);
            });
        });
        if (result) {
            outputChannel.appendLine(`[OK] Found ok in PATH: ${result}`);
            return result;
        }
    } catch {
        // not in PATH, continue
    }

    // Step 3-4: cached or download
    const assetName = platformAssetName();
    if (!assetName) {
        throw new Error(
            `Unsupported platform ${process.platform}/${process.arch}. ` +
            'Please install ok manually from https://github.com/ok-ai/ok/releases',
        );
    }

    const cacheDir = context.globalStorageUri.fsPath;
    const cachedPath = path.join(cacheDir, assetName);

    // Step 3: check cache
    try {
        await fs.promises.access(cachedPath, fs.constants.R_OK);
        outputChannel.appendLine(`[OK] Using cached binary: ${cachedPath}`);
        return cachedPath;
    } catch {
        // not cached, download
    }

    // Step 4: download
    outputChannel.appendLine(`[OK] Downloading ok binary (${assetName})...`);
    const downloadUrl = `https://github.com/${GITHUB_REPO}/releases/latest/download/${assetName}`;

    await fs.promises.mkdir(cacheDir, { recursive: true });
    await downloadFile(downloadUrl, cachedPath);

    // Set executable bit on Unix
    if (process.platform !== 'win32') {
        await fs.promises.chmod(cachedPath, 0o755);
    }

    outputChannel.appendLine(`[OK] Binary downloaded to: ${cachedPath}`);
    return cachedPath;
}

async function stopOkServer(): Promise<void> {
    if (!okProcess) return;
    outputChannel.appendLine('[OK] Stopping server...');
    okProcess.kill('SIGTERM');
    // Force kill after 5s
    setTimeout(() => {
        if (okProcess) {
            okProcess.kill('SIGKILL');
            okProcess = undefined;
        }
    }, 5000);
}

// ---------------------------------------------------------------------------
// HTTP helpers for OK API
// ---------------------------------------------------------------------------

async function okApi(method: string, path: string, body?: unknown): Promise<unknown> {
    return new Promise((resolve, reject) => {
        const data = body ? JSON.stringify(body) : undefined;
        const options: http.RequestOptions = {
            hostname: '127.0.0.1',
            port: okPort,
            path,
            method,
            headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
            timeout: 60000,
        };

        const req = http.request(options, (res) => {
            let result = '';
            res.on('data', (chunk: Buffer) => { result += chunk.toString(); });
            res.on('end', () => {
                try {
                    resolve(result ? JSON.parse(result) : result);
                } catch {
                    resolve(result);
                }
            });
        });
        req.on('error', reject);
        req.on('timeout', () => { req.destroy(); reject(new Error('timeout')); });
        if (data) req.write(data);
        req.end();
    });
}

// SSE stream for real-time responses
async function okSubmitSSE(
    input: string,
    onText: (text: string) => void,
    onDone: () => void,
    onError: (err: string) => void,
): Promise<void> {
    return new Promise((resolve) => {
        const req = http.request({
            hostname: '127.0.0.1',
            port: okPort,
            path: '/events',
            method: 'GET',
            headers: { 'Accept': 'text/event-stream' },
        }, (res) => {
            let buffer = '';
            res.on('data', (chunk: Buffer) => {
                buffer += chunk.toString();
                const lines = buffer.split('\n');
                buffer = lines.pop() ?? '';

                for (const line of lines) {
                    if (line.startsWith('data: ')) {
                        try {
                            const evt = JSON.parse(line.slice(6));
                            if (evt.kind === 'text' && evt.text) {
                                onText(evt.text);
                            } else if (evt.kind === 'tool_dispatch') {
                                onText(`\n🔧 ${evt.text || 'using tool…'}\n`);
                            } else if (evt.kind === 'tool_result' && evt.text) {
                                onText(`\n✓ ${evt.text.slice(0, 200)}\n`);
                            } else if (evt.kind === 'turn_done') {
                                onDone();
                            } else if (evt.kind === 'notice' && evt.level === 'error') {
                                onError(evt.text || 'error');
                            }
                        } catch {
                            // ignore malformed events
                        }
                    } else if (line.startsWith('event: done')) {
                        onDone();
                    }
                }
            });

            res.on('end', () => { onDone(); resolve(); });
            res.on('error', (err) => { onError(err.message); resolve(); });
        });

        req.on('error', (err) => { onError(err.message); resolve(); });
        req.end();

        // Submit the task after connecting to SSE
        okApi('POST', '/submit', { input }).catch((err) => {
            onError(`Submit failed: ${err.message}`);
            resolve();
        });
    });
}

// ---------------------------------------------------------------------------
// cliManager implementation for webviewProvider
// ---------------------------------------------------------------------------

function createCliManager() {
    return {
        request: async (method: string, params?: unknown): Promise<unknown> => {
            switch (method) {
                case 'task.run': {
                    const p = params as { task?: string } | undefined;
                    if (!p?.task) throw new Error('task is required');
                    return new Promise((resolve, reject) => {
                        okSubmitSSE(
                            p.task!,
                            (text) => webviewProvider?.postMessage({ type: 'stream', text }),
                            () => webviewProvider?.postMessage({ type: 'status', text: 'idle' }),
                            (err) => { webviewProvider?.postMessage({ type: 'error', text: err }); reject(new Error(err)); },
                        ).then(resolve).catch(reject);
                    });
                }
                case 'task.cancel':
                    await okApi('POST', '/cancel');
                    return { cancelled: true };
                case 'task.history':
                    return await okApi('GET', '/history');
                case 'task.context':
                    return await okApi('GET', '/context');
                case 'task.compact':
                    return await okApi('POST', '/compact');
                case 'task.new':
                    return await okApi('POST', '/new');
                case 'task.plan':
                    return await okApi('POST', '/plan', params);
                default:
                    throw new Error(`Unknown method: ${method}`);
            }
        },
        notify: (method: string, params?: unknown): void => {
            okApi('POST', `/${method}`, params).catch((err) => {
                outputChannel.appendLine(`[OK] notify ${method} failed: ${err.message}`);
            });
        },
    };
}

// ---------------------------------------------------------------------------
// CodeLens Provider — adds "Explain" / "Fix" lenses on functions and classes
// ---------------------------------------------------------------------------

class OkCodeLensProvider implements vscode.CodeLensProvider {
    private _onDidChangeCodeLenses = new vscode.EventEmitter<void>();
    public readonly onDidChangeCodeLenses = this._onDidChangeCodeLenses.event;

    provideCodeLenses(
        document: vscode.TextDocument,
        _token: vscode.CancellationToken,
    ): vscode.CodeLens[] {
        const lenses: vscode.CodeLens[] = [];
        const text = document.getText();

        // Match function/method/class definitions
        const patterns: { regex: RegExp }[] = [
            { regex: /^(export\s+)?(async\s+)?function\s+(\w+)/gm },
            { regex: /^(export\s+)?class\s+(\w+)/gm },
            { regex: /^(export\s+)?const\s+(\w+)\s*=\s*(async\s+)?\(/gm },
        ];

        for (const { regex } of patterns) {
            let match: RegExpExecArray | null;
            while ((match = regex.exec(text)) !== null) {
                const name = match[3] || match[2];
                if (!name || name === 'if' || name === 'for' || name === 'while') continue;
                const pos = document.positionAt(match.index);
                const range = new vscode.Range(pos, pos);

                lenses.push(new vscode.CodeLens(range, {
                    title: '$(search) Explain',
                    command: 'ok.explainCodeLens',
                    arguments: [document.uri, range],
                }));
                lenses.push(new vscode.CodeLens(range, {
                    title: '$(wrench) Fix',
                    command: 'ok.fixCodeLens',
                    arguments: [document.uri, range],
                }));
            }
        }

        return lenses;
    }
}

// ---------------------------------------------------------------------------
// InlineCompletionItemProvider — AI-powered inline code suggestions
// ---------------------------------------------------------------------------

class OkInlineCompletionProvider implements vscode.InlineCompletionItemProvider {
    async provideInlineCompletionItems(
        document: vscode.TextDocument,
        position: vscode.Position,
        _context: vscode.InlineCompletionContext,
        _token: vscode.CancellationToken,
    ): Promise<vscode.InlineCompletionItem[]> {
        // Only trigger on newline, '.', '(', or after typing 3+ chars
        const line = document.lineAt(position.line);
        const textBeforeCursor = line.text.substring(0, position.character);
        const trimmed = textBeforeCursor.trimEnd();

        if (trimmed.length > 0 &&
            !trimmed.endsWith('.') &&
            !trimmed.endsWith('(') &&
            trimmed.length < 3) {
            return [];
        }

        // Build context: current file prefix + surrounding lines
        const startLine = Math.max(0, position.line - 10);
        const prefixLines: string[] = [];
        for (let i = startLine; i <= position.line; i++) {
            prefixLines.push(document.lineAt(i).text);
        }
        const prefix = prefixLines.join('\n');

        try {
            // Use SSE streaming to get a completion: submit, collect first text chunk, cancel
            const completion = await new Promise<string>((resolve) => {
                let resolved = false;
                okSubmitSSE(
                    `Complete this code. Return ONLY the completion (no explanation):\n\`\`\`\n${prefix}\n\`\`\``,
                    (text: string) => {
                        if (!resolved) {
                            resolved = true;
                            // Extract only the first line of the completion
                            const cleaned = text
                                .replace(/^```[\w]*\n?/gm, '')
                                .replace(/\n?```$/gm, '')
                                .trim();
                            resolve(cleaned.split('\n')[0] || '');
                        }
                    },
                    () => { if (!resolved) { resolved = true; resolve(''); } },
                    () => { if (!resolved) { resolved = true; resolve(''); } },
                ).catch(() => { if (!resolved) { resolved = true; resolve(''); } });

                // Timeout after 5s
                setTimeout(() => { if (!resolved) { resolved = true; resolve(''); } }, 5000);
            });

            if (!completion || completion === trimmed) return [];
            return [new vscode.InlineCompletionItem(completion)];
        } catch {
            return [];
        }
    }
}

// ---------------------------------------------------------------------------
// VS Code Extension lifecycle
// ---------------------------------------------------------------------------

export async function activate(context: vscode.ExtensionContext) {
    outputChannel = vscode.window.createOutputChannel('OK Agent');
    outputChannel.appendLine('OK Agent activating...');

    // --- Ensure ok binary is available ---
    try {
        resolvedBinaryPath = await ensureBinary(context);
    } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : String(err);
        outputChannel.appendLine(`Failed to locate ok binary: ${msg}. Will try PATH as fallback.`);
        // resolvedBinaryPath stays undefined → okPath() falls back to config/PATH
    }

    // --- Start OK server ---
    try {
        okPort = await startOkServer();
        outputChannel.appendLine(`OK server running on port ${okPort}`);
    } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : String(err);
        outputChannel.appendLine(`Failed to start OK server: ${msg}`);
        vscode.window.showWarningMessage(
            `OK server could not start: ${msg}. ` +
            `Make sure the ok CLI is installed.\n` +
            `• Auto-download: restart VS Code after the extension activates.\n` +
            `• Manual: install from github.com/ok-ai/ok/releases and add to PATH.`,
        );
    }

    // --- Register sidebar view ---
    const cliManager = createCliManager();
    webviewProvider = new OkWebviewViewProvider(context.extensionUri, cliManager);
    context.subscriptions.push(
        vscode.window.registerWebviewViewProvider('ok.sidebar', webviewProvider),
    );

    // --- Register commands ---
    context.subscriptions.push(
        vscode.commands.registerCommand('ok.chat', () => {
            vscode.commands.executeCommand('workbench.view.extension.ok-sidebar');
        }),

        vscode.commands.registerCommand('ok.explain', async () => {
            const editor = vscode.window.activeTextEditor;
            if (!editor) return;
            const selection = editor.document.getText(editor.selection);
            if (!selection) {
                vscode.window.showInformationMessage('Select code to explain');
                return;
            }
            try {
                const result = await okApi('POST', '/submit', {
                    input: `Explain this code concisely:\n\`\`\`\n${selection}\n\`\`\``,
                }) as string;
                showResult('OK: Explain', typeof result === 'string' ? result : JSON.stringify(result));
            } catch (err: unknown) {
                const msg = err instanceof Error ? err.message : String(err);
                vscode.window.showErrorMessage(`OK: ${msg}`);
            }
        }),

        vscode.commands.registerCommand('ok.fix', async () => {
            const editor = vscode.window.activeTextEditor;
            if (!editor) return;
            const diagnostics = vscode.languages.getDiagnostics(editor.document.uri);
            if (diagnostics.length === 0) {
                vscode.window.showInformationMessage('No problems found in this file');
                return;
            }
            const filePath = editor.document.uri.fsPath;
            const problems = diagnostics.slice(0, 5).map(d =>
                `Line ${d.range.start.line + 1}: ${d.message}`
            ).join('\n');
            try {
                await okApi('POST', '/submit', {
                    input: `Fix these problems in ${filePath}:\n${problems}\n\nApply minimal fixes.`,
                });
                vscode.window.showInformationMessage('OK is fixing problems… check the sidebar.');
            } catch (err: unknown) {
                const msg = err instanceof Error ? err.message : String(err);
                vscode.window.showErrorMessage(`OK: ${msg}`);
            }
        }),

        vscode.commands.registerCommand('ok.review', async () => {
            const gitExt = vscode.extensions.getExtension('vscode.git');
            if (!gitExt) {
                // Fallback: try git diff via child_process
                try {
                    const diff = await execCommand('git diff');
                    if (!diff) {
                        vscode.window.showInformationMessage('No changes to review');
                        return;
                    }
                    await okApi('POST', '/submit', {
                        input: `Review these changes concisely:\n\`\`\`diff\n${diff.slice(0, 8000)}\n\`\`\``,
                    });
                    vscode.window.showInformationMessage('OK is reviewing changes… check the sidebar.');
                } catch {
                    vscode.window.showInformationMessage('No changes to review');
                }
            } else {
                vscode.window.showInformationMessage('Use OK: Start Chat and enter /review');
            }
        }),

        vscode.commands.registerCommand('ok.newSession', async () => {
            try {
                await okApi('POST', '/new');
                webviewProvider?.postMessage({ type: 'startSession' });
                vscode.window.showInformationMessage('OK: New session started');
            } catch (err: unknown) {
                const msg = err instanceof Error ? err.message : String(err);
                vscode.window.showErrorMessage(`OK: ${msg}`);
            }
        }),

        vscode.commands.registerCommand('ok.showOutput', () => {
            outputChannel.show();
        }),
    );

    // --- Register CodeLens provider ---
    const codeLensProvider = new OkCodeLensProvider();
    context.subscriptions.push(
        vscode.languages.registerCodeLensProvider(
            { scheme: 'file', language: 'typescript' }, codeLensProvider,
        ),
        vscode.languages.registerCodeLensProvider(
            { scheme: 'file', language: 'javascript' }, codeLensProvider,
        ),
        vscode.languages.registerCodeLensProvider(
            { scheme: 'file', language: 'typescriptreact' }, codeLensProvider,
        ),
        vscode.languages.registerCodeLensProvider(
            { scheme: 'file', language: 'javascriptreact' }, codeLensProvider,
        ),
        vscode.languages.registerCodeLensProvider(
            { scheme: 'file', language: 'python' }, codeLensProvider,
        ),
        vscode.languages.registerCodeLensProvider(
            { scheme: 'file', language: 'go' }, codeLensProvider,
        ),
    );

    // --- CodeLens command handlers ---
    context.subscriptions.push(
        vscode.commands.registerCommand('ok.explainCodeLens', async (uri: vscode.Uri, range: vscode.Range) => {
            const editor = await vscode.window.showTextDocument(uri);
            const code = editor.document.getText(
                new vscode.Range(range.start, editor.document.lineAt(range.start.line + 50).range.end),
            );
            try {
                await okApi('POST', '/submit', {
                    input: `Explain this code concisely:\n\`\`\`\n${code.slice(0, 4000)}\n\`\`\``,
                });
                vscode.window.showInformationMessage('OK is explaining… check the sidebar.');
            } catch (err: unknown) {
                const msg = err instanceof Error ? err.message : String(err);
                vscode.window.showErrorMessage(`OK: ${msg}`);
            }
        }),

        vscode.commands.registerCommand('ok.fixCodeLens', async (uri: vscode.Uri, _range: vscode.Range) => {
            await vscode.window.showTextDocument(uri);
            const diagnostics = vscode.languages.getDiagnostics(uri);
            if (diagnostics.length === 0) {
                vscode.window.showInformationMessage('No problems found — code looks good!');
                return;
            }
            const problems = diagnostics.slice(0, 5).map(d =>
                `Line ${d.range.start.line + 1}: ${d.message}`
            ).join('\n');
            try {
                await okApi('POST', '/submit', {
                    input: `Fix these problems in the file:\n${problems}\n\nApply minimal fixes.`,
                });
                vscode.window.showInformationMessage('OK is fixing… check the sidebar.');
            } catch (err: unknown) {
                const msg = err instanceof Error ? err.message : String(err);
                vscode.window.showErrorMessage(`OK: ${msg}`);
            }
        }),
    );

    // --- Register InlineCompletion provider ---
    context.subscriptions.push(
        vscode.languages.registerInlineCompletionItemProvider(
            { scheme: 'file' },
            new OkInlineCompletionProvider(),
        ),
    );

    // --- Status bar item ---
    const statusBar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
    statusBar.text = '$(hubot) OK';
    statusBar.tooltip = 'OK Agent — Click to chat';
    statusBar.command = 'ok.chat';
    statusBar.show();
    context.subscriptions.push(statusBar);

    outputChannel.appendLine('OK Agent activated successfully');
}

function showResult(title: string, text: string) {
    // Truncate for notification
    const short = text.length > 200 ? text.slice(0, 200) + '…' : text;
    vscode.window.showInformationMessage(short, { modal: false }, 'Open Chat').then((choice) => {
        if (choice === 'Open Chat') {
            vscode.commands.executeCommand('workbench.view.extension.ok-sidebar');
        }
    });
}

function execCommand(cmd: string): Promise<string> {
    return new Promise((resolve, reject) => {
        cp.exec(cmd, { maxBuffer: 1024 * 1024 }, (err, stdout) => {
            if (err) reject(err);
            else resolve(stdout);
        });
    });
}

export async function deactivate() {
    outputChannel?.appendLine('OK Agent deactivating...');
    await stopOkServer();
}
