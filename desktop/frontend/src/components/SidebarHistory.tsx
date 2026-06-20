import { useState } from "react";
import { Pencil, Trash2, Check, X, MessageSquare, History } from "lucide-react";
import { t, useT } from "../lib/i18n";
import type { SessionMeta } from "../lib/types";

export function SidebarHistory({
  sessions,
  onResume,
  onDelete,
  onRename,
  onClose,
}: {
  sessions: SessionMeta[];
  onResume: (path: string) => void;
  onDelete: (path: string) => void;
  onRename: (path: string, title: string) => void;
  onClose: () => void;
}) {
  const tr = useT();
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [confirming, setConfirming] = useState<string | null>(null);

  const startRename = (s: SessionMeta) => {
    setConfirming(null);
    setEditing(s.path);
    setDraft(s.title || s.preview || "");
  };
  const commitRename = (path: string) => {
    onRename(path, draft.trim());
    setEditing(null);
  };

  const groups: { label: string; items: SessionMeta[] }[] = [];
  for (const s of sessions) {
    const label = dayLabel(s.modTime);
    const last = groups[groups.length - 1];
    if (last && last.label === label) last.items.push(s);
    else groups.push({ label, items: [s] });
  }

  return (
    <div className="sb-panel">
      <div className="sb-panel__head">
        <History size={15} />
        <span>{tr("history.title")}</span>
        <button className="sb-panel__close" onClick={onClose}><X size={13} /></button>
      </div>
      <div className="sb-panel__body">
        {sessions.length === 0 ? (
          <div className="sb-panel__empty">{tr("history.empty")}</div>
        ) : (
          groups.map((g) => (
            <div key={g.label} className="sb-hist-group">
              <div className="sb-panel__group">{g.label}</div>
              {g.items.map((s) => (
                <div className={`sb-hist-card${s.current ? " sb-hist-card--current" : ""}`} key={s.path}>
                  {editing === s.path ? (
                    <input
                      className="sb-hist-rename"
                      autoFocus
                      value={draft}
                      onChange={(e) => setDraft(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") commitRename(s.path);
                        if (e.key === "Escape") setEditing(null);
                      }}
                      onBlur={() => commitRename(s.path)}
                      placeholder={tr("history.namePlaceholder")}
                    />
                  ) : (
                    <button className="sb-hist-main" onClick={() => onResume(s.path)}>
                      <div className="sb-hist-main__top">
                        <MessageSquare size={12} className="sb-hist-main__icon" />
                        <span className="sb-hist-main__title">
                          {s.title || s.preview || tr("history.emptySession")}
                        </span>
                      </div>
                      <div className="sb-hist-main__meta">
                        <span>{tr(s.turns === 1 ? "history.turnOne" : "history.turnOther", { n: s.turns })}</span>
                        <span>·</span>
                        <span className="sb-hist-main__time">{timeLabel(s.modTime)}</span>
                        {s.current && <span className="sb-hist-main__badge">{tr("history.current")}</span>}
                      </div>
                    </button>
                  )}

                  {editing !== s.path && (
                    <div className="sb-hist-actions">
                      {confirming === s.path ? (
                        <>
                          <button className="sb-hist-btn sb-hist-btn--danger" title={tr("history.confirmDelete")}
                            onClick={() => { onDelete(s.path); setConfirming(null); }}>
                            <Check size={12} />
                          </button>
                          <button className="sb-hist-btn" title={tr("common.cancel")} onClick={() => setConfirming(null)}>
                            <X size={12} />
                          </button>
                        </>
                      ) : (
                        <>
                          <button className="sb-hist-btn" title={tr("history.rename")} onClick={() => startRename(s)}>
                            <Pencil size={11} />
                          </button>
                          {!s.current && (
                            <button className="sb-hist-btn sb-hist-btn--danger" title={tr("common.delete")}
                              onClick={() => setConfirming(s.path)}>
                              <Trash2 size={11} />
                            </button>
                          )}
                        </>
                      )}
                    </div>
                  )}
                </div>
              ))}
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function dayLabel(ms: number): string {
  const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((startOfDay(new Date()) - startOfDay(new Date(ms))) / 86_400_000);
  if (days <= 0) return t("history.today");
  if (days === 1) return t("history.yesterday");
  return new Date(ms).toLocaleDateString();
}

function timeLabel(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
