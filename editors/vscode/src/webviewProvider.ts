import * as vscode from 'vscode';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** Messages sent FROM the extension host TO the webview */
export interface ExtensionMessage {
  type: string;
  [key: string]: unknown;
}

/** Messages sent FROM the webview TO the extension host */
interface WebviewMessage {
  type: string;
  [key: string]: unknown;
}

// ---------------------------------------------------------------------------
// OkWebviewViewProvider
// ---------------------------------------------------------------------------

export class OkWebviewViewProvider implements vscode.WebviewViewProvider {
  private _view: vscode.WebviewView | undefined;

  constructor(
    private readonly _extensionUri: vscode.Uri,
    private readonly _cliManager: { request: (method: string, params?: unknown) => Promise<unknown>; notify: (method: string, params?: unknown) => void },
  ) {}

  // -----------------------------------------------------------------------
  // vscode.WebviewViewProvider
  // -----------------------------------------------------------------------

  resolveWebviewView(
    webviewView: vscode.WebviewView,
    _context: vscode.WebviewViewResolveContext,
    _token: vscode.CancellationToken,
  ): void {
    this._view = webviewView;

    webviewView.webview.options = {
      enableScripts: true,
      localResourceRoots: [this._extensionUri],
    };

    webviewView.webview.html = this._getHtmlTemplate();

    // Handle messages from the webview
    webviewView.webview.onDidReceiveMessage(
      (message: WebviewMessage) => {
        void this._handleWebviewMessage(message);
      },
    );
  }

  // -----------------------------------------------------------------------
  // Public API — called by extension.ts
  // -----------------------------------------------------------------------

  /** Send a message from the extension host to the webview */
  public postMessage(message: ExtensionMessage): void {
    if (this._view) {
      void this._view.webview.postMessage(message);
    }
  }

  // -----------------------------------------------------------------------
  // Private helpers
  // -----------------------------------------------------------------------

  private async _handleWebviewMessage(message: WebviewMessage): Promise<void> {
    switch (message.type) {
      case 'ready': {
        // Webview is ready — send a hello
        this.postMessage({ type: 'status', text: 'connected' });
        break;
      }

      case 'submitTask': {
        const text = message.text as string | undefined;
        if (!text) break;
        try {
          this.postMessage({ type: 'status', text: 'running…' });
          const result = await this._cliManager.request('task.run', { task: text });
          this.postMessage({ type: 'result', data: result });
          this.postMessage({ type: 'status', text: 'idle' });
        } catch (err: unknown) {
          const msg = err instanceof Error ? err.message : String(err);
          this.postMessage({ type: 'error', text: msg });
          this.postMessage({ type: 'status', text: 'idle' });
        }
        break;
      }

      case 'cancel': {
        try {
          await this._cliManager.request('task.cancel');
          this.postMessage({ type: 'status', text: 'idle' });
        } catch {
          // ignore
        }
        break;
      }

      default: {
        console.warn('[ok:webview] unknown message type:', message.type);
      }
    }
  }

  // -----------------------------------------------------------------------
  // HTML template
  // -----------------------------------------------------------------------

  private _getHtmlTemplate(): string {
    return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <style>
    :root {
      --bg: #1e1e1e;
      --surface: #252526;
      --border: #3c3c3c;
      --text: #cccccc;
      --text-dim: #888888;
      --accent: #4fc3f7;
      --err: #f44747;
      --ok: #6a9955;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      background: var(--bg);
      color: var(--text);
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Helvetica Neue', sans-serif;
      font-size: 13px;
      padding: 12px;
      display: flex;
      flex-direction: column;
      height: 100vh;
      overflow: hidden;
    }
    h1 {
      font-size: 16px;
      font-weight: 600;
      margin-bottom: 8px;
      display: flex;
      align-items: center;
      gap: 8px;
    }
    h1 .logo {
      display: inline-block;
      width: 18px; height: 18px;
      background: var(--accent);
      clip-path: polygon(50% 0%, 100% 50%, 50% 100%, 0% 50%);
    }
    #status-bar {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 6px 10px;
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: 4px;
      margin-bottom: 10px;
      font-size: 12px;
    }
    #status-indicator {
      width: 8px; height: 8px;
      border-radius: 50%;
      background: var(--ok);
    }
    #status-indicator.busy { background: #e8a838; }
    #status-indicator.error { background: var(--err); }
    #status-text { color: var(--text-dim); }
    #history {
      flex: 1;
      overflow-y: auto;
      margin-bottom: 10px;
      padding: 8px;
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: 4px;
    }
    #history::-webkit-scrollbar { width: 6px; }
    #history::-webkit-scrollbar-thumb { background: var(--border); border-radius: 3px; }
    .msg {
      padding: 6px 8px;
      margin-bottom: 6px;
      border-radius: 4px;
      line-height: 1.4;
      white-space: pre-wrap;
      word-break: break-word;
    }
    .msg.user {
      background: #2a2d2e;
      border-left: 3px solid var(--accent);
    }
    .msg.agent {
      background: #1e2a2e;
      border-left: 3px solid var(--ok);
    }
    .msg.error {
      background: #2e1e1e;
      border-left: 3px solid var(--err);
      color: var(--err);
    }
    .msg .label {
      font-size: 11px;
      font-weight: 600;
      margin-bottom: 2px;
      color: var(--text-dim);
    }
    #input-row {
      display: flex;
      gap: 6px;
    }
    #input-row textarea {
      flex: 1;
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: 4px;
      color: var(--text);
      padding: 8px;
      font-family: inherit;
      font-size: 13px;
      resize: none;
      outline: none;
    }
    #input-row textarea:focus {
      border-color: var(--accent);
    }
    #input-row button {
      background: var(--accent);
      color: #111;
      border: none;
      border-radius: 4px;
      padding: 8px 16px;
      font-weight: 600;
      cursor: pointer;
      font-size: 13px;
      white-space: nowrap;
    }
    #input-row button:hover { opacity: 0.9; }
    #input-row button:disabled { opacity: 0.4; cursor: default; }
    #cancel-btn {
      background: transparent;
      color: var(--text-dim);
      border: 1px solid var(--border);
      border-radius: 4px;
      padding: 8px 10px;
      cursor: pointer;
      font-size: 12px;
      display: none;
    }
    #cancel-btn:hover { color: var(--err); border-color: var(--err); }
  </style>
</head>
<body>
  <h1><span class="logo"></span>OK Agent</h1>

  <div id="status-bar">
    <span id="status-indicator"></span>
    <span id="status-text">idle</span>
  </div>

  <div id="history"></div>

  <div id="input-row">
    <textarea id="task-input" rows="2" placeholder="Describe a task for the OK agent…" enterkeyhint="send"></textarea>
    <button id="send-btn" title="Send task">Send</button>
    <button id="cancel-btn" title="Cancel running task">✕</button>
  </div>

  <script>
    (function () {
      const vscode = acquireVsCodeApi();

      // DOM refs
      const statusIndicator = document.getElementById('status-indicator');
      const statusText     = document.getElementById('status-text');
      const historyEl      = document.getElementById('history');
      const taskInput      = document.getElementById('task-input');
      const sendBtn        = document.getElementById('send-btn');
      const cancelBtn      = document.getElementById('cancel-btn');

      let busy = false;

      // ---- helpers ----

      function setStatus(state) {
        statusIndicator.className = '';
        if (state === 'busy') {
          statusIndicator.classList.add('busy');
          statusText.textContent = 'running…';
          busy = true;
        } else if (state === 'error') {
          statusIndicator.classList.add('error');
          statusText.textContent = 'error';
          busy = false;
        } else {
          statusText.textContent = 'idle';
          busy = false;
        }
        sendBtn.disabled = busy;
        cancelBtn.style.display = busy ? 'inline-block' : 'none';
      }

      function addMessage(role, content) {
        const div = document.createElement('div');
        div.className = 'msg ' + role;
        const label = document.createElement('div');
        label.className = 'label';
        label.textContent = role === 'user' ? 'You' : role === 'agent' ? 'Agent' : 'Error';
        div.appendChild(label);
        const text = document.createElement('div');
        text.textContent = content;
        div.appendChild(text);
        historyEl.appendChild(div);
        historyEl.scrollTop = historyEl.scrollHeight;
      }

      function sendTask(text) {
        if (!text || busy) return;
        addMessage('user', text);
        taskInput.value = '';
        setStatus('busy');
        vscode.postMessage({ type: 'submitTask', text: text });
      }

      // ---- event listeners ----

      sendBtn.addEventListener('click', function () {
        sendTask(taskInput.value.trim());
      });

      taskInput.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' && !e.shiftKey) {
          e.preventDefault();
          sendTask(taskInput.value.trim());
        }
      });

      cancelBtn.addEventListener('click', function () {
        vscode.postMessage({ type: 'cancel' });
        setStatus('idle');
      });

      // ---- handle messages from extension host ----

      window.addEventListener('message', function (event) {
        const msg = event.data;
        switch (msg.type) {
          case 'status':
            setStatus(msg.text === 'running…' ? 'busy' : msg.text);
            break;
          case 'result':
            addMessage('agent', JSON.stringify(msg.data, null, 2));
            setStatus('idle');
            break;
          case 'error':
            addMessage('error', msg.text || 'Unknown error');
            setStatus('error');
            break;
          case 'startSession':
            historyEl.innerHTML = '';
            setStatus('idle');
            break;
        }
      });

      // ---- tell the host we are ready ----
      vscode.postMessage({ type: 'ready' });
    })();
  </script>
</body>
</html>`;
  }
}
