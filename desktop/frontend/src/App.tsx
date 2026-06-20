import { useCallback, useRef, useState } from "react";
import { Plus, MessageSquare, Settings as SettingsIcon, Loader2 } from "lucide-react";
import { useT } from "./lib/i18n";
import { useController } from "./lib/useController";
import { Transcript } from "./components/Transcript";
import { Composer } from "./components/Composer";
import { TodoPanel } from "./components/TodoPanel";
import { ApprovalModal } from "./components/ApprovalModal";
import { AskCard } from "./components/AskCard";
import { SettingsModal } from "./components/SettingsModal";
import { SidebarHistory } from "./components/SidebarHistory";
import { parseTodos } from "./lib/tools";
import type { SessionMeta } from "./lib/types";

const ANIM_DURATION = 180; // ms — matches CSS transition/animation

export default function App() {
  const {
    state, send, cancel, approve, answerQuestion,
    newSession, refreshMeta, pickWorkspace, setModel,
    listSessions, resumeSession, deleteSession, renameSession,
  } = useController();
  const t = useT();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsClosing, setSettingsClosing] = useState(false);
  const [histView, setHistView] = useState<SessionMeta[] | null>(null);
  const [histClosing, setHistClosing] = useState(false);
  const [resuming, setResuming] = useState(false);
  const histToggleLock = useRef(false);

  const switchModel = useCallback(async (name: string) => {
    await setModel(name);
  }, [setModel]);

  const todoItem = (() => {
    for (let i = state.items.length - 1; i >= 0; i--) {
      const it = state.items[i];
      if (it.kind === "tool" && it.name === "todo_write" && !it.parentId) return it;
    }
    return null;
  })();
  const todos = todoItem ? parseTodos(todoItem.args) : [];
  const [dismissedTodo, setDismissedTodo] = useState<string | null>(null);
  const showTodos = !!todoItem && todoItem.id !== dismissedTodo && todos.length > 0 &&
    todos.some((t) => t.status !== "completed");

  const handleSend = useCallback((text: string) => {
    const m = /^\/model\s+(\S+)$/.exec(text.trim());
    if (m) { void switchModel(m[1]); return; }
    send(text);
  }, [switchModel, send]);

  const switchFolder = useCallback(async () => { await pickWorkspace(); }, [pickWorkspace]);

  // ─── Toggle helpers ───
  const closeSettingsWithAnim = useCallback(() => {
    if (settingsClosing) return;
    setSettingsClosing(true);
    setTimeout(() => {
      setSettingsOpen(false);
      setSettingsClosing(false);
    }, ANIM_DURATION);
  }, [settingsClosing]);

  const toggleSettings = useCallback(() => {
    if (settingsOpen && !settingsClosing) {
      closeSettingsWithAnim();
    } else if (!settingsOpen) {
      setSettingsOpen(true);
    }
  }, [settingsOpen, settingsClosing, closeSettingsWithAnim]);

  const toggleHistory = useCallback(async () => {
    if (histToggleLock.current) return;
    if (histView && !histClosing) {
      histToggleLock.current = true;
      setHistClosing(true);
      setTimeout(() => {
        setHistView(null);
        setHistClosing(false);
        histToggleLock.current = false;
      }, ANIM_DURATION);
    } else if (!histView) {
      setHistView(await listSessions());
    }
  }, [histView, histClosing, listSessions]);

  const closeHistoryWithAnim = useCallback(() => {
    if (histClosing || histToggleLock.current) return;
    histToggleLock.current = true;
    setHistClosing(true);
    setTimeout(() => {
      setHistView(null);
      setHistClosing(false);
      histToggleLock.current = false;
    }, ANIM_DURATION);
  }, [histClosing]);

  // Resume a session with a smooth transition: animate the history panel out
  // first, show a loading indicator in the main area, then load the session.
  const onResumeSession = useCallback(async (path: string) => {
    if (histClosing || histToggleLock.current) return;
    histToggleLock.current = true;
    setResuming(true);
    // Close panel with animation
    setHistClosing(true);
    // Wait for the slide-out animation to finish before tearing down the panel
    // and loading the new session — eliminates the "snap-away" feel.
    await new Promise((r) => setTimeout(r, ANIM_DURATION));
    setHistView(null);
    setHistClosing(false);
    histToggleLock.current = false;
    // Load the session (async — may take a moment for disk I/O)
    await resumeSession(path);
    setResuming(false);
  }, [histClosing, resumeSession]);

  const onDeleteSession = useCallback(async (path: string) => {
    await deleteSession(path);
    setHistView(await listSessions());
  }, [deleteSession, listSessions]);

  const onRenameSession = useCallback(async (path: string, title: string) => {
    await renameSession(path, title);
    setHistView(await listSessions());
  }, [renameSession, listSessions]);

  return (
    <div className="app">
      <nav className={`sidebar ${histView || histClosing ? "sidebar--expanded" : ""}`}>
        <div className="sidebar__icons">
          <div className="sidebar__icons-top">
            <button className="sidebar__btn sidebar__btn--logo" onClick={newSession} title={t("topbar.newSession")}>
              <Plus size={20} />
            </button>
            <div className="sidebar__sep" />
            <button
              className={`sidebar__btn ${histView ? "sidebar__btn--active" : ""}`}
              onClick={() => void toggleHistory()}
              title={t("topbar.history")}
            >
              <MessageSquare size={18} />
            </button>
          </div>
          <div className="sidebar__icons-bottom">
            <button className={`sidebar__btn ${settingsOpen ? "sidebar__btn--active" : ""}`}
              onClick={toggleSettings} title={t("topbar.settings")}>
              <SettingsIcon size={18} />
            </button>
          </div>
        </div>
        {(histView || histClosing) && (
          <div className={`sidebar__panel ${histClosing ? "sidebar__panel--closing" : ""}`}>
            {histView && (
              <SidebarHistory
                sessions={histView}
                onResume={onResumeSession}
                onDelete={onDeleteSession}
                onRename={onRenameSession}
                onClose={closeHistoryWithAnim}
              />
            )}
          </div>
        )}
      </nav>

      <div className="main-area">
        {state.meta?.startupErr && (
          <div className="banner banner--error">{t("topbar.startupError", { msg: state.meta.startupErr })}</div>
        )}
        <main className="main">
          {resuming ? (
            <div className="loading-overlay">
              <Loader2 size={24} className="loading-overlay__spinner" />
              <span className="loading-overlay__text">{t("history.resuming")}</span>
            </div>
          ) : (
            <Transcript items={state.items} onPrompt={send} />
          )}
        </main>
        <footer className="footer">
          {showTodos && <TodoPanel todos={todos} onDismiss={() => setDismissedTodo(todoItem!.id)} />}
          <Composer
            running={state.running}
            meta={state.meta}
            usage={state.usage}
            balance={state.balance}
            onSend={handleSend}
            onCancel={cancel}
            onCycleMode={() => {}}
            onSwitchModel={switchModel}
            onPickFolder={() => void switchFolder()}
          />
        </footer>
      </div>

      {state.approval && (
        <ApprovalModal approval={state.approval}
          onAnswer={(allow, session) => {
            approve(state.approval!.id, allow, session);
          }}
        />
      )}

      {state.ask && (
        <AskCard ask={state.ask} onAnswer={answerQuestion}
          onDismiss={() => answerQuestion(state.ask!.id, [])}
        />
      )}

      {(settingsOpen || settingsClosing) &&
        <SettingsModal closing={settingsClosing} onClose={closeSettingsWithAnim} onChanged={() => void refreshMeta()} />
      }
    </div>
  );
}
