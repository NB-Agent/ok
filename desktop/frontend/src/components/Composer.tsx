import { useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { ArrowUp, ChevronDown, FolderOpen, Square, Mic, MicOff } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { BalanceInfo, CommandInfo, DirEntry, Meta, ModelInfo, SlashArgItem, SlashArgsResult, WireUsage } from "../lib/types";
import { SlashMenu } from "./SlashMenu";
import { ArgMenu } from "./ArgMenu";
import { FileMenu } from "./FileMenu";

function nowRate(u?: WireUsage): number | null {
  if (!u) return null;
  // Use session-cumulative values when available — steadier across turns.
  let hit = u.sessionCacheHitTokens || u.cacheHitTokens;
  let miss = u.sessionCacheMissTokens || u.cacheMissTokens;
  let denom = hit + miss;
  if (denom === 0) denom = u.promptTokens;
  if (denom <= 0) return null;
  return Math.round((hit / denom) * 100);
}

export function Composer({
  running,
  meta,
  usage,
  balance,
  onSend,
  onCancel,
  onCycleMode,
  onSwitchModel,
  onPickFolder,
}: {
  running: boolean;
  meta?: Meta;
  usage?: WireUsage;
  balance?: BalanceInfo;
  onSend: (text: string) => void;
  // Returns the un-sent text when cancelling before the server replied (so it can
  // be restored to the input); undefined for a normal cancel.
  onCancel: () => string | undefined;
  onCycleMode: () => void;
  onSwitchModel: (name: string) => void;
  onPickFolder: () => void;
}) {
  const t = useT();
  const [text, setText] = useState("");
  const [active, setActive] = useState(0);
  const [dismissed, setDismissed] = useState(false);
  const [listening, setListening] = useState(false);
  const recognitionRef = useRef<any>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);

  // --- 模型下拉 ---
  const [modelOpen, setModelOpen] = useState(false);
  const [models, setModels] = useState<ModelInfo[]>([]);
  useEffect(() => {
    if (modelOpen) app.Models().then((m) => setModels(m ?? [])).catch(() => {});
  }, [modelOpen]);
  const pickModel = (name: string) => { setModelOpen(false); onSwitchModel(name); };

  // --- status line ---
  const modelLabel = meta?.label || "";
  const balanceLabel = balance?.display || "";
  const cwdLabel = meta?.cwd || "";
  const cachePct = nowRate(usage);

  // --- slash commands (whole-input "/token") ---
  const [commands, setCommands] = useState<CommandInfo[]>([]);
  useEffect(() => {
    app.Commands().then(setCommands).catch(() => {});
  }, []);

  const slashQuery = useMemo(() => {
    if (!text.startsWith("/") || /\s/.test(text)) return null;
    return text.slice(1).toLowerCase();
  }, [text]);
  const slashMatches = useMemo(
    () => (slashQuery === null ? [] : commands.filter((c) => c.name.toLowerCase().includes(slashQuery)).slice(0, 8)),
    [slashQuery, commands],
  );

  // --- slash argument completion ("/cmd <args>") --- mirrors the CLI: once past
  // the command word, the backend suggests sub-commands (/skill → list/show/…,
  // /mcp → add/remove, /model → refs). Fetched from app.SlashArgs.
  const [argRes, setArgRes] = useState<SlashArgsResult | null>(null);
  useEffect(() => {
    if (!text.startsWith("/") || !/\s/.test(text)) {
      setArgRes(null);
      return;
    }
    let live = true;
    app
      .SlashArgs(text)
      .then((r) => {
        if (!live) return;
        // Drop suggestions that wouldn't change the input — the token is already
        // fully typed (e.g. "/skill list" offering "list"). Otherwise the menu
        // lingers on a complete command and Enter keeps "accepting" a no-op
        // instead of sending. (Defense-in-depth: the backend filters these too.)
        // r.items can arrive as null (an empty Go slice serializes to JSON null),
        // so guard before filtering — otherwise the throw is swallowed and the
        // stale menu from the previous keystroke lingers (the /skill list bug).
        const useful = (r.items ?? []).filter((it) => text.slice(0, r.from) + it.insert !== text);
        setArgRes(useful.length > 0 ? { items: useful, from: r.from } : null);
        setActive(0);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [text]);

  // --- @ file references (token at the end of the text) ---
  // atRaw is everything after a trailing "@token"; atDir is its path up to the
  // last "/", atFrag the part after. The menu lists one directory level (atDir)
  // and filters by atFrag — descending one level per pick.
  const atRaw = useMemo(() => {
    const m = /(?:^|\s)@([^\s]*)$/.exec(text);
    return m ? m[1] : null;
  }, [text]);
  const atDir = useMemo(() => {
    if (atRaw === null) return "";
    const slash = atRaw.lastIndexOf("/");
    return slash >= 0 ? atRaw.slice(0, slash + 1) : "";
  }, [atRaw]);
  const atFrag = useMemo(() => {
    if (atRaw === null) return "";
    const slash = atRaw.lastIndexOf("/");
    return (slash >= 0 ? atRaw.slice(slash + 1) : atRaw).toLowerCase();
  }, [atRaw]);

  const [entries, setEntries] = useState<DirEntry[]>([]);
  const dirCache = useRef<Record<string, DirEntry[]>>({});
  useEffect(() => {
    if (atRaw === null) return;
    const cached = dirCache.current[atDir];
    if (cached) {
      setEntries(cached);
      return;
    }
    let live = true;
    app
      .ListDir(atDir)
      .then((es) => {
        const list = es ?? [];
        dirCache.current[atDir] = list;
        if (live) setEntries(list);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
    // re-fetch only when the menu opens or the directory level changes
  }, [atRaw === null, atDir]);
  const atMatches = useMemo(
    () => (atRaw === null ? [] : entries.filter((e) => e.name.toLowerCase().includes(atFrag)).slice(0, 10)),
    [atRaw, atFrag, entries],
  );

  // --- which menu (if any) is open --- (slash command names win; then slash
  // arguments; then @-refs — they're rarely valid at once)
  const menuMode: "slash" | "slasharg" | "at" | null =
    slashMatches.length > 0 && !dismissed
      ? "slash"
      : argRes && argRes.items.length > 0 && !dismissed
        ? "slasharg"
        : atMatches.length > 0 && !dismissed
          ? "at"
          : null;
  const count =
    menuMode === "slash"
      ? slashMatches.length
      : menuMode === "slasharg"
        ? argRes!.items.length
        : menuMode === "at"
          ? atMatches.length
          : 0;

  // Reset highlight + un-dismiss whenever the active query changes.
  useEffect(() => {
    setActive(0);
    setDismissed(false);
  }, [slashQuery, atRaw]);

  const setTextCaretEnd = (next: string) => {
    setText(next);
    requestAnimationFrame(() => {
      const ta = taRef.current;
      if (ta) {
        ta.focus();
        ta.selectionStart = ta.selectionEnd = next.length;
      }
    });
  };

  const submit = () => {
    const t = text.trim();
    if (!t) return;
    onSend(t);
    setText("");
  };

  // ——— speech recognition (Web Speech API, Chromium only) ———

  const startListening = () => {
    const SpeechRecognitionCtor = window.SpeechRecognition || window.webkitSpeechRecognition;
    if (!SpeechRecognitionCtor) return;
    if (recognitionRef.current) return;
    const recognition = new SpeechRecognitionCtor();
    recognition.continuous = false;
    recognition.interimResults = false;
    recognition.lang = navigator.language || "en-US";
    recognition.onresult = (event: ISpeechRecognitionEvent) => {
      const transcript = event.results[0][0].transcript;
      setText((prev) => (prev ? prev + " " + transcript : transcript));
    };
    recognition.onerror = () => { setListening(false); recognitionRef.current = null; };
    recognition.onend = () => { setListening(false); recognitionRef.current = null; };
    recognition.start();
    recognitionRef.current = recognition;
    setListening(true);
  };

  const stopListening = () => {
    if (recognitionRef.current) {
      recognitionRef.current.stop();
      recognitionRef.current = null;
    }
    setListening(false);
  };

  const toggleListening = () => {
    if (listening) { stopListening(); return; }
    startListening();
  };

  // handleCancel stops the in-flight turn; if it was cancelled before the server
  // replied, the just-sent text is handed back so we drop it back into the input.
  const handleCancel = () => {
    const restored = onCancel();
    if (typeof restored === "string") setTextCaretEnd(restored);
  };

  const pickCommand = (c: CommandInfo) => setTextCaretEnd("/" + c.name + " ");

  const pickEntry = (e: DirEntry) => {
    const atPos = text.length - (atRaw?.length ?? 0) - 1; // index of '@'
    const prefix = text.slice(0, atPos);
    // A directory keeps the menu open (trailing "/"); a file completes it (space).
    setTextCaretEnd(prefix + "@" + atDir + e.name + (e.isDir ? "/" : " "));
  };

  // pickArg replaces just the current token with the suggestion. A "descend" item
  // (e.g. "/skill show ") ends with a space, so the effect re-fetches the next
  // level; a terminal item leaves the menu (next fetch returns nothing).
  const pickArg = (it: SlashArgItem) => {
    if (!argRes) return;
    setTextCaretEnd(text.slice(0, argRes.from) + it.insert);
  };

  const pickActive = () => {
    if (menuMode === "slash") pickCommand(slashMatches[active]);
    else if (menuMode === "slasharg" && argRes) pickArg(argRes.items[active]);
    else if (menuMode === "at") pickEntry(atMatches[active]);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    const composing = e.nativeEvent.isComposing;

    // Shift+Tab cycles the input mode (normal → plan → YOLO → normal). Handled
    // before the menus so it works even while one is open.
    if (e.key === "Tab" && e.shiftKey && !composing) {
      e.preventDefault();
      onCycleMode();
      return;
    }

    if (menuMode && !composing) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setActive((i) => (i + 1) % count);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setActive((i) => (i - 1 + count) % count);
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        pickActive();
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setDismissed(true);
        return;
      }
    }

    // Enter sends; Shift+Enter newline. isComposing guards IME (pinyin) confirms.
    if (e.key === "Enter" && !e.shiftKey && !composing) {
      e.preventDefault();
      submit();
    }
    // Esc interrupts the in-flight turn (matches the Stop button's hint), and
    // restores the text if the server hadn't replied yet.
    if (e.key === "Escape" && running) {
      e.preventDefault();
      handleCancel();
    }
  };

  return (
    <div className="composer-wrap">
      {menuMode === "slash" && (
        <SlashMenu items={slashMatches} activeIndex={active} onPick={pickCommand} onHover={setActive} />
      )}
      {menuMode === "slasharg" && argRes && (
        <ArgMenu items={argRes.items} activeIndex={active} onPick={pickArg} onHover={setActive} />
      )}
      {menuMode === "at" && <FileMenu items={atMatches} activeIndex={active} onPick={pickEntry} onHover={setActive} />}

      {/* Status line — 模型 · 余额 · 路径，各自明确标注 */}
      <div className="composer__meta">
        {/* 模型 — 当前模型名 + 下拉切换 */}
        <div className="composer__model">
          <button
            className="composer__model-btn"
            onClick={() => setModelOpen((v) => !v)}
          >
            <span className="composer__meta-label">模型</span>
            <span className="composer__model-name">{modelLabel || "—"}</span>
            <ChevronDown size={10} className={`composer__model-arrow ${modelOpen ? "composer__model-arrow--open" : ""}`} />
          </button>
          {modelOpen && (
            <>
              <div className="composer__model-backdrop" onClick={() => setModelOpen(false)} />
              <div className="composer__model-menu">
                {models.length === 0 && <div className="composer__model-empty">无可用模型</div>}
                {models.map((m) => (
                  <button
                    key={m.ref}
                    className={`composer__model-item ${m.current ? "composer__model-item--active" : ""}`}
                    onClick={() => pickModel(m.ref)}
                  >
                    <span>{m.model}</span>
                    {m.current && <span className="composer__model-check">✓</span>}
                  </button>
                ))}
              </div>
            </>
          )}
        </div>

        <span className="composer__meta-sep">·</span>

        {/* 缓存命中率 — 始终占位 */}
        <span className="composer__meta-badge">
          <span className="composer__meta-label">缓存</span>
          <span className="composer__meta-val">{cachePct !== null ? `${cachePct}%` : "—"}</span>
        </span>

        <span className="composer__meta-sep">·</span>

        {/* 余额 — 高亮 */}
        <span className={`composer__meta-badge ${balanceLabel ? "composer__meta-badge--balance" : ""}`}>
          <span className="composer__meta-label">{balanceLabel ? "余额" : "💰"}</span>
          <span className="composer__meta-val">{balanceLabel || "—"}</span>
        </span>

        <span className="composer__meta-sep">·</span>

        {/* 项目路径 — 完整显示 */}
        {cwdLabel ? (
          <button
            className="composer__meta-badge composer__meta-badge--cwd"
            onClick={onPickFolder}
            title={cwdLabel}
          >
            <FolderOpen size={12} />
            <span className="composer__meta-label">项目</span>
            <span className="composer__meta-val">{cwdLabel}</span>
          </button>
        ) : (
          <span className="composer__meta-badge composer__meta-badge--cwd">
            <FolderOpen size={12} />
            <span className="composer__meta-label">项目</span>
            <span className="composer__meta-val">—</span>
          </span>
        )}
      </div>

      <div className="composer">
        <textarea
          ref={taRef}
          className="composer__input"
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            // auto-expand: reset height to shrink on delete, then grow to scrollHeight
            const ta = e.target;
            ta.style.height = "auto";
            ta.style.height = Math.min(ta.scrollHeight, 200) + "px";
          }}
          onKeyDown={onKeyDown}
          placeholder={t("composer.placeholder")}
          rows={1}
        />
        <button
          className={`composer__btn composer__btn--mic ${listening ? "composer__btn--mic-active" : ""}`}
          onClick={toggleListening}
          title={listening ? "Listening… click to stop" : "Voice input (microphone)"}
        >
          {listening ? <MicOff size={14} /> : <Mic size={14} />}
        </button>
        {running ? (
          <button className="composer__btn composer__btn--stop" onClick={handleCancel} title={t("composer.stop")}>
            <Square size={14} fill="currentColor" />
          </button>
        ) : (
          <button
            className="composer__btn composer__btn--send"
            onClick={submit}
            disabled={!text.trim()}
            title={t("composer.send")}
          >
            <ArrowUp size={16} />
          </button>
        )}
      </div>
    </div>
  );
}
