import { useEffect, useState, useCallback, useMemo } from "react";
import { Cpu, Puzzle, Palette, X, Plus, Globe, Code, MessageSquare } from "lucide-react";
import { useI18n } from "../lib/i18n";
import { applyTheme, getTheme, type Theme } from "../lib/theme";
import type { BotStatus, PluginView, ProviderView, SettingsView } from "../lib/types";
import { app } from "../lib/bridge";

type Tab = "models" | "integrations" | "appearance";

const TABS: { id: Tab; label: string; icon: React.ReactNode }[] = [
  { id: "models",       label: "模型",  icon: <Cpu size={15} /> },
  { id: "integrations", label: "集成", icon: <Puzzle size={15} /> },
  { id: "appearance",   label: "外观", icon: <Palette size={15} /> },
];

export function SettingsModal({ closing, onClose, onChanged }: { closing?: boolean; onClose: () => void; onChanged: () => void }) {
  const { t } = useI18n();
  const [s, setS] = useState<SettingsView | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("models");
  const [theme, setThemeState] = useState<Theme>(getTheme());

  const reload = async () => setS(await app.Settings().catch(() => null));
  useEffect(() => { void reload(); }, []);

  const apply = async (fn: () => Promise<void>) => {
    setBusy(true); setErr(null);
    try { await fn(); await reload(); onChanged(); }
    catch (e) { setErr(String((e as Error)?.message ?? e)); }
    finally { setBusy(false); }
  };

  return (
    <div className={`settings-backdrop ${closing ? "settings-backdrop--closing" : ""}`} onClick={onClose}>
      <div className={`settings-modal ${closing ? "settings-modal--closing" : ""}`} onClick={(e) => e.stopPropagation()}>
        <div className="settings-modal__head">
          <span>{t("settings.title")}</span>
          <button className="settings-close-btn" onClick={onClose}><X size={18} /></button>
        </div>
        {!s ? (
          <div className="settings-modal__loading">{t("settings.loading")}</div>
        ) : (
          <div className="settings-modal__body">
            {err && <div className="settings-modal__banner settings-modal__banner--err">{err}</div>}
            <nav className="settings-modal__tabs">
              {TABS.map((tabDef) => (
                <button key={tabDef.id}
                  className={`settings-modal__tab ${tab === tabDef.id ? "settings-modal__tab--active" : ""}`}
                  onClick={() => setTab(tabDef.id)}>
                  {tabDef.icon}<span>{tabDef.label}</span>
                </button>
              ))}
            </nav>
            <div className="settings-modal__content">
              {tab === "models" && <ModelsTab s={s} busy={busy} apply={apply} />}
              {tab === "integrations" && <IntegrationsTab s={s} busy={busy} apply={apply} />}
              {tab === "appearance" && <AppearanceTab theme={theme} onTheme={setThemeState} />}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// ─── helpers ───

function allRefs(s: SettingsView): string[] {
  const out: string[] = [];
  for (const p of s.providers) for (const m of p.models) out.push(`${p.name}/${m}`);
  return out;
}

function toRef(model: string, s: SettingsView): string {
  if (!model || model.includes("/")) return model;
  const byName = s.providers.find((p) => p.name === model);
  if (byName) return `${byName.name}/${byName.default || byName.models[0] || ""}`;
  const byModel = s.providers.find((p) => p.models.includes(model));
  if (byModel) return `${byModel.name}/${model}`;
  return model;
}

type SectionProps = { s: SettingsView; busy: boolean; apply: (fn: () => Promise<void>) => Promise<void> };

// ─── Models Tab ──────────────────────────────────────────────────────────────

function ModelsTab({ s, busy, apply }: SectionProps) {
  const { t } = useI18n();
  const refs = allRefs(s);
  const defaultProvider = toRef(s.defaultModel, s).split("/")[0];
  const [editing, setEditing] = useState<string | null>(null);

  return (
    <div className="st-tab">
      <div className="st-group">
        <div className="st-group__label">{t("settings.defaultModel")}</div>
        <select className="st-select st-full" value={toRef(s.defaultModel, s)} disabled={busy}
          onChange={(e) => void apply(() => app.SetDefaultModel(e.target.value))}>
          {refs.map((r) => <option key={r} value={r}>{r}</option>)}
        </select>
      </div>
      <div className="st-group">
        <div className="st-group__label">{t("settings.plannerModel")}</div>
        <select className="st-select st-full" value={s.plannerModel ? toRef(s.plannerModel, s) : ""} disabled={busy}
          onChange={(e) => void apply(() => app.SetPlannerModel(e.target.value || ""))}>
          <option value="">{t("settings.plannerNone")}</option>
          {refs.map((r) => <option key={r} value={r}>{r}</option>)}
        </select>
      </div>
      <div className="st-divider" />
      <div className="st-group__label">{t("settings.modelsProviders")}</div>
      {s.providers.map((p) => editing === p.name ? (
        <ProviderEditor key={p.name} initial={p} kinds={s.providerKinds} busy={busy}
          onCancel={() => setEditing(null)}
          onSave={(pv) => apply(() => app.SaveProvider(pv)).then(() => setEditing(null))} />
      ) : (
        <ProviderCard key={p.name} p={p} busy={busy} defaultProvider={defaultProvider}
          onEdit={() => setEditing(p.name)}
          onDelete={() => void apply(() => app.DeleteProvider(p.name))}
          onSetKey={async (v) => { await apply(() => app.SetProviderKey(p.apiKeyEnv, v)); }} />
      ))}
      {editing === "__new__" ? (
        <ProviderEditor kinds={s.providerKinds} busy={busy}
          onCancel={() => setEditing(null)}
          onSave={(pv) => apply(() => app.SaveProvider(pv)).then(() => setEditing(null))} />
      ) : (
        <button className="st-add-btn" disabled={busy} onClick={() => setEditing("__new__")}>
          <Plus size={14} /> {t("settings.addProvider")}
        </button>
      )}
    </div>
  );
}

function ProviderCard({ p, busy, defaultProvider, onEdit, onDelete, onSetKey }: {
  p: ProviderView; busy: boolean; defaultProvider: string;
  onEdit: () => void; onDelete: () => void; onSetKey: (v: string) => Promise<void>;
}) {
  const { t } = useI18n(); const [showKey, setShowKey] = useState(false); const [keyVal, setKeyVal] = useState("");
  return (
    <div className="st-card">
      <div className="st-card__top">
        <div className="st-card__info">
          <span className="st-card__name">{p.name}</span>
          <span className="st-card__tag">{p.kind}</span>
          <span className={`st-card__badge ${p.keySet ? "st-card__badge--ok" : "st-card__badge--warn"}`}>
            {p.keySet ? t("settings.keySet") : t("settings.noKey")}
          </span>
        </div>
        <div className="st-card__actions">
          <button className="st-icon-btn" disabled={busy} onClick={onEdit}><Cpu size={13} /></button>
          <button className="st-icon-btn st-icon-btn--danger" disabled={busy || defaultProvider === p.name}
            title={defaultProvider === p.name ? t("settings.cantDeleteDefault") : ""} onClick={onDelete}>
            <X size={13} />
          </button>
        </div>
      </div>
      <div className="st-card__meta">
        <span><Globe size={10} /> {p.baseUrl}</span>
        <span><Code size={10} /> {p.models.join(", ")}</span>
      </div>
      <div className="st-card__key">
        {showKey ? (
          <div className="st-card__key-row">
            <input className="st-input st-grow" type="password" placeholder={t("settings.setKey", { env: p.apiKeyEnv })}
              value={keyVal} onChange={(e) => setKeyVal(e.target.value)} />
            <button className="st-btn-sm" disabled={busy || !keyVal.trim()}
              onClick={() => { void onSetKey(keyVal.trim()); setKeyVal(""); }}>{t("settings.saveKey")}</button>
            <button className="st-btn-ghost" onClick={() => setShowKey(false)}>{t("common.cancel")}</button>
          </div>
        ) : p.apiKeyEnv ? (
          <button className="st-btn-ghost st-btn-xs" onClick={() => setShowKey(true)}>
            {p.keySet ? t("settings.updateKey") : t("settings.setKey", { env: p.apiKeyEnv })}
          </button>
        ) : null}
      </div>
    </div>
  );
}

function ProviderEditor({ initial, kinds, busy, onCancel, onSave }: {
  initial?: ProviderView; kinds: string[]; busy: boolean;
  onCancel: () => void; onSave: (p: ProviderView) => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(initial?.name ?? "");
  const [kind, setKind] = useState(initial?.kind ?? kinds[0] ?? "openai");
  const [baseUrl, setBaseUrl] = useState(initial?.baseUrl ?? "");
  const [models, setModels] = useState((initial?.models ?? []).join(", "));
  const [apiKeyEnv, setApiKeyEnv] = useState(initial?.apiKeyEnv ?? "");
  const kindOptions = kind && !kinds.includes(kind) ? [kind, ...kinds] : kinds;
  const save = () => {
    const ms = models.split(",").map((m) => m.trim()).filter(Boolean);
    onSave({ name: name.trim(), kind: kind.trim() || kinds[0] || "openai", baseUrl: baseUrl.trim(),
      models: ms, default: ms[0] ?? "", apiKeyEnv: apiKeyEnv.trim(), keySet: initial?.keySet ?? false });
  };
  return (
    <div className="st-editor">
      <input className="st-input" placeholder={t("settings.providerName")} value={name}
        onChange={(e) => setName(e.target.value)} disabled={!!initial} />
      <div className="st-row">
        <select className="st-select st-grow" value={kind} onChange={(e) => setKind(e.target.value)}>
          {kindOptions.map((k) => <option key={k} value={k}>{k}</option>)}
        </select>
        <input className="st-input st-grow" placeholder={t("settings.providerBaseUrl")} value={baseUrl}
          onChange={(e) => setBaseUrl(e.target.value)} />
      </div>
      <input className="st-input" placeholder={t("settings.providerModels")} value={models}
        onChange={(e) => setModels(e.target.value)} />
      <input className="st-input" placeholder={t("settings.providerApiKeyEnv")} value={apiKeyEnv}
        onChange={(e) => setApiKeyEnv(e.target.value)} />
      <div className="st-editor__actions">
        <button className="st-btn-ghost st-btn-sm" onClick={onCancel} disabled={busy}>{t("common.cancel")}</button>
        <button className="st-btn-amber st-btn-sm" onClick={save}
          disabled={busy || !name.trim() || !baseUrl.trim()}>{t("common.save")}</button>
      </div>
    </div>
  );
}

// ─── Integrations Tab ────────────────────────────────────────────────────────

const BOT_PLATFORMS = [
  { name: "Slack",     desc: "Slack bot — 消息通知、频道互动",     env: ["SLACK_BOT_TOKEN", "SLACK_SIGNING_SECRET"] },
  { name: "Discord",   desc: "Discord bot — 社区交互、命令响应",   env: ["DISCORD_BOT_TOKEN", "DISCORD_PUBLIC_KEY"] },
  { name: "Telegram",  desc: "Telegram bot — 即时通讯",           env: ["TELEGRAM_BOT_TOKEN"] },
  { name: "企业微信",  desc: "WeCom bot — 企业内部通讯",          env: ["WECHAT_CORP_ID", "WECHAT_AGENT_ID", "WECHAT_SECRET"] },
  { name: "飞书",      desc: "Feishu/Lark bot — 团队协作",        env: ["FEISHU_APP_ID", "FEISHU_APP_SECRET"] },
  { name: "WhatsApp",  desc: "WhatsApp Cloud API bot",            env: ["WHATSAPP_PHONE_ID", "WHATSAPP_TOKEN"] },
  { name: "钉钉",      desc: "DingTalk bot — 企业通讯",           env: ["DINGTALK_WEBHOOK_URL", "DINGTALK_WEBHOOK_TOKEN"] },
];

function IntegrationsTab({ s, busy, apply }: SectionProps) {
  const { t } = useI18n();
  const [pluginEditor, setPluginEditor] = useState<string | null>(null);
  const [bots, setBots] = useState<BotStatus[]>([]);
  const [botValues, setBotValues] = useState<Record<string, Record<string, string>>>({});

  const refreshBots = useCallback(async () => {
    try { setBots(await app.RunningBots()); } catch {}
  }, []);

  useEffect(() => { void refreshBots(); }, []);
  const botMap = useMemo(() => {
    const m = new Map<string, BotStatus>();
    for (const b of bots) m.set(b.name, b);
    return m;
  }, [bots]);

  const handleSetBotEnv = async (botName: string, key: string, value: string) => {
    await apply(() => app.SetBotEnv(key, value));
    setBotValues((prev) => ({ ...prev, [botName]: { ...prev[botName], [key]: "" } }));
    await refreshBots();
  };

  return (
    <div className="st-tab">
      {/* MCP Plugins */}
      <div className="st-group__label">{t("settings.mcpPlugins")}</div>
      <p className="st-hint" style={{marginBottom:10}}>{t("settings.mcpHint")}</p>
      {s.plugins.length === 0 && <p className="st-hint" style={{marginBottom:8,fontStyle:"italic"}}>{t("settings.noPlugins")}</p>}
      {s.plugins.map((p) => (
        <div className="st-card" key={p.name}>
          <div className="st-card__top">
            <div className="st-card__info">
              <span className="st-card__name">{p.name}</span>
              <span className="st-card__tag">{p.type || "stdio"}</span>
              {p.keySet && <span className="st-card__badge st-card__badge--ok">{t("settings.keySet")}</span>}
            </div>
            <button className="st-icon-btn st-icon-btn--danger" disabled={busy}
              onClick={() => void apply(() => app.DeletePlugin(p.name))}>
              <X size={13} />
            </button>
          </div>
          <div className="st-card__meta">
            {p.command && <span><Code size={10} /> {p.command} {p.args}</span>}
            {p.url && <span><Globe size={10} /> {p.url}</span>}
          </div>
        </div>
      ))}
      {pluginEditor === "__new__" ? (
        <PluginEditor busy={busy} onCancel={() => setPluginEditor(null)}
          onSave={(pl) => apply(() => app.SavePlugin(pl)).then(() => setPluginEditor(null))} />
      ) : (
        <button className="st-add-btn" disabled={busy} onClick={() => setPluginEditor("__new__")}>
          <Plus size={14} /> {t("settings.addPlugin")}
        </button>
      )}

      <div className="st-divider" />

      {/* Bot Platforms — 填入凭据保存，点击启动 */}
      <div className="st-group__label">{t("settings.botPlatforms")}</div>
      <p className="st-hint" style={{marginBottom:10}}>{t("settings.botHint")}</p>
      <div className="st-bot-list">
        {BOT_PLATFORMS.map((bp) => {
          const st = botMap.get(bp.name);
          return (
            <details className="st-bot-item" key={bp.name}>
              <summary className="st-bot-summary">
                <MessageSquare size={13} className="st-card__icon" />
                <span className="st-card__name">{bp.name}</span>
                {st?.keySet && <span className="st-prov__badge st-prov__badge--ok" style={{fontSize:9,marginLeft:4}}>已配置</span>}
                {st?.running && <span className="st-prov__badge st-prov__badge--ok" style={{fontSize:9}}>运行中</span>}
                <span className="st-hint" style={{marginLeft:"auto",fontSize:10}}>展开配置</span>
              </summary>
              <div className="st-bot-body">
                <p className="st-hint" style={{marginBottom:8}}>{bp.desc}</p>
                {bp.env.map((envVar) => (
                  <div key={envVar} className="st-row" style={{marginBottom:4}}>
                    <input className="st-input st-grow" type="password"
                      placeholder={envVar}
                      value={botValues[bp.name]?.[envVar] ?? ""}
                      onChange={(e) => setBotValues((prev) => ({
                        ...prev, [bp.name]: { ...prev[bp.name], [envVar]: e.target.value },
                      }))}
                    />
                    <button className="st-btn-sm" disabled={busy || !botValues[bp.name]?.[envVar]}
                      onClick={() => void handleSetBotEnv(bp.name, envVar, botValues[bp.name]?.[envVar] ?? "")}>
                      保存
                    </button>
                  </div>
                ))}
                <div className="st-row" style={{marginTop:8}}>
                  {st?.running ? (
                    <button className="st-btn-sm" disabled={busy}
                      onClick={() => void apply(() => app.StopBot(bp.name)).then(refreshBots)}>
                      停止
                    </button>
                  ) : (
                    <button className="st-btn-amber" disabled={busy || !st?.keySet}
                      onClick={() => void apply(() => app.StartBot(bp.name)).then(refreshBots)}>
                      启动机器人
                    </button>
                  )}
                </div>
              </div>
            </details>
          );
        })}
      </div>

      <div className="st-divider" />

      {/* Sandbox */}
      <div className="st-group__label">{t("settings.sandboxTitle")}</div>
      <SandboxSection s={s} busy={busy} apply={apply} />
    </div>
  );
}

function PluginEditor({ busy, onCancel, onSave }: {
  busy: boolean; onCancel: () => void; onSave: (p: PluginView) => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState("");
  const [type, setType] = useState("stdio");
  const [command, setCommand] = useState("");
  const [args, setArgs] = useState("");
  const [url, setUrl] = useState("");
  const save = () => onSave({ name: name.trim(), type, command: command.trim(), args: args.trim(), url: url.trim(), keySet: false });
  return (
    <div className="st-editor">
      <input className="st-input" placeholder={t("settings.pluginName")} value={name} onChange={(e) => setName(e.target.value)} />
      <div className="st-row">
        <select className="st-select st-narrow" value={type} onChange={(e) => setType(e.target.value)}>
          <option value="stdio">stdio</option>
          <option value="http">http</option>
          <option value="sse">sse</option>
        </select>
        {type === "stdio" ? (
          <>
            <input className="st-input st-grow" placeholder={t("settings.pluginCommand")} value={command}
              onChange={(e) => setCommand(e.target.value)} />
            <input className="st-input st-grow" placeholder={t("settings.pluginArgs")} value={args}
              onChange={(e) => setArgs(e.target.value)} />
          </>
        ) : (
          <input className="st-input st-full" placeholder={t("settings.pluginUrl")} value={url}
            onChange={(e) => setUrl(e.target.value)} />
        )}
      </div>
      <div className="st-editor__actions">
        <button className="st-btn-ghost st-btn-sm" onClick={onCancel} disabled={busy}>{t("common.cancel")}</button>
        <button className="st-btn-amber st-btn-sm" onClick={save}
          disabled={busy || !name.trim() || (type === "stdio" && !command.trim()) || (type !== "stdio" && !url.trim())}>
          {t("common.save")}
        </button>
      </div>
    </div>
  );
}

function SandboxSection({ s, busy, apply }: SectionProps) {
  const { t } = useI18n();
  const [network, setNetwork] = useState(s.sandbox.network);
  const [bash, setBashState] = useState(s.sandbox.bash);
  return (
    <>
      <div className="st-row">
        <span className="st-label">{t("settings.bashSandboxLabel")}</span>
        <select className="st-select" value={bash} disabled={busy}
          onChange={(e) => { setBashState(e.target.value); void apply(() => app.SetSandbox(bash, network)); }}>
          <option value="enforce">{t("settings.bashEnforce")}</option>
          <option value="off">{t("settings.bashOff")}</option>
        </select>
      </div>
      <div className="st-toggle-row">
        <span>{t("settings.allowNetwork")}</span>
        <button className={`toggle ${network ? "toggle--on" : ""}`} disabled={busy}
          onClick={() => { setNetwork(!network); void apply(() => app.SetSandbox(bash, !network)); }}
          role="switch" aria-checked={network}>
          <span className="toggle__knob" />
        </button>
      </div>
    </>
  );
}

// ─── Appearance Tab ──────────────────────────────────────────────────────────

const ALL_LANGUAGES: Record<string, string> = {
  "": "Auto",
  ar: "العربية", bn: "বাংলা", de: "Deutsch", el: "Ελληνικά",
  en: "English", es: "Español", fa: "فارسی", fr: "Français",
  ha: "Hausa", hi: "हिन्दी", id: "Bahasa Indonesia", it: "Italiano",
  ja: "日本語", ko: "한국어", ms: "Bahasa Melayu", nl: "Nederlands",
  pl: "Polski", pt: "Português", ro: "Română", ru: "Русский",
  sw: "Kiswahili", ta: "தமிழ்", th: "ไทย", tl: "Tagalog",
  tr: "Türkçe", uk: "Українська", ur: "اردو", vi: "Tiếng Việt",
  zh: "中文", zu: "isiZulu",
};

function AppearanceTab({ theme, onTheme }: { theme: Theme; onTheme: (t: Theme) => void }) {
  const { t, pref, setPref } = useI18n();
  return (
    <div className="st-tab">
      <div className="st-group">
        <div className="st-group__label">{t("settings.theme")}</div>
        <div className="st-theme-picker">
          {(["auto", "light", "dark"] as const).map((opt) => (
            <button key={opt} className={`st-theme-opt ${theme === opt ? "st-theme-opt--active" : ""}`}
              onClick={() => { applyTheme(opt); onTheme(opt); }}>
              <span className="st-theme-opt__preview">
                {opt === "dark" && <span className="st-theme-preview st-theme-preview--dark" />}
                {opt === "light" && <span className="st-theme-preview st-theme-preview--light" />}
                {opt === "auto" && <><span className="st-theme-preview st-theme-preview--dark" /><span className="st-theme-preview st-theme-preview--light" /></>}
              </span>
              {opt === "auto" ? t("settings.themeAuto") : opt === "light" ? t("settings.themeLight") : t("settings.themeDark")}
            </button>
          ))}
        </div>
      </div>
      <div className="st-group">
        <div className="st-group__label">{t("settings.language")}</div>
        <select className="st-select st-full" value={pref} onChange={(e) => setPref(e.target.value)}>
          {Object.entries(ALL_LANGUAGES).map(([code, label]) => (
            <option key={code} value={code}>{label}</option>
          ))}
        </select>
      </div>
    </div>
  );
}
