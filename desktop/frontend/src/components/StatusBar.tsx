import { ChevronsUpDown } from "lucide-react";
import { useEffect, useState } from "react";
import { app } from "../lib/bridge";
import type { BalanceInfo, Meta, ModelInfo, WireUsage } from "../lib/types";

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

export function StatusBar({
  meta,
  usage,
  balance,
  onSwitchModel,
  onPickFolder,
}: {
  meta?: Meta;
  usage?: WireUsage;
  balance?: BalanceInfo;
  onSwitchModel: (name: string) => void;
  onPickFolder: () => void;
}) {
  const nowPct = nowRate(usage);
  const modelLabel = meta?.label || "";
  const cacheLabel = nowPct !== null ? `缓存 ${nowPct}%` : "";
  const balanceLabel = balance?.available && balance.display ? balance.display : "";
  const cwdLabel = meta?.cwd || "";

  const parts = [modelLabel, cacheLabel, balanceLabel, cwdLabel].filter(Boolean);
  const display = parts.join(" · ");

  // inline model switcher popover
  const [open, setOpen] = useState(false);
  const [models, setModels] = useState<ModelInfo[]>([]);
  useEffect(() => {
    if (open) app.Models().then((m) => setModels(m ?? [])).catch(() => {});
  }, [open]);

  const pick = (name: string) => {
    setOpen(false);
    onSwitchModel(name);
  };

  return (
    <div className="statusbar">
      <div className="statusbar__inner">
        <button className="statusbar__trigger" onClick={() => setOpen((v) => !v)}>
          <span className="statusbar__text">{display}</span>
          <ChevronsUpDown size={10} />
        </button>

        <button className="statusbar__cwdbtn" onClick={onPickFolder} title={cwdLabel}>
          📁
        </button>
      </div>

      {open && (
        <>
          <div className="modelsw__backdrop" onClick={() => setOpen(false)} />
          <div className="modelsw__menu" role="listbox">
            {models.length === 0 && <div className="modelsw__empty">无模型</div>}
            {models.map((m) => (
              <button
                key={m.ref}
                role="option"
                aria-selected={m.current}
                className={`modelsw__item ${m.current ? "modelsw__item--current" : ""}`}
                onClick={() => pick(m.ref)}
              >
                <span className="modelsw__model">{m.model}</span>
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
