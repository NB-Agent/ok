// Types matching the Go wire format (desktop/wire.go). Each maps to a JSON
// shape the kernel emits or receives — the single source of truth.

export type Theme = "auto" | "light" | "dark";
export type Mode = "normal" | "plan" | "yolo";

// ─── event stream (wire.go wireEvent → wireUsage → wireTool) ───

export interface WireEvent {
  kind: string;
  text?: string;
  reasoning?: string;
  level?: string;       // "info" | "warn" (notice events)
  tool?: WireTool;
  usage?: WireUsage;
  approval?: WireApproval;
  ask?: WireAsk;
  err?: string;         // turn_done error message
}

export interface WireTool {
  id?: string;
  name: string;
  args?: string;
  output?: string;
  err?: string;         // matches Go wireTool.Err → json:"err"
  readOnly: boolean;
  truncated?: boolean;
  partial?: boolean;
  parentId?: string;
}

export interface WireUsage {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  cacheHitTokens: number;
  cacheMissTokens: number;
  reasoningTokens?: number;
  sessionCacheHitTokens: number;
  sessionCacheMissTokens: number;
  costUsd?: number;
}

export interface WireApproval {
  id: string;
  tool: string;
  subject: string;
}

export interface WireAsk {
  id: string;
  questions: WireAskQuestion[];
}

export interface WireAskQuestion {
  id: string;
  header?: string;
  prompt: string;
  options: { label: string; description?: string }[];
  multi?: boolean;
}

export interface QuestionAnswer {
  questionId: string;
  selected: string[];
}

// ─── session / history (desktop/app.go) ───

export interface SessionMeta {
  path: string;
  title?: string;
  preview: string;
  turns: number;
  modTime: number;  // unix millis from Go
  current?: boolean;
}

export interface HistoryMessage {
  role: string;
  content: string;
}

// ─── memory (desktop/app.go) ───

export interface MemoryView {
  scopes: { scope: string; path: string }[];
  facts: { name: string; description: string; type: string; body: string }[];
  available: boolean;
  docs: { scope: string; path: string; body: string }[];
  storeDir: string;
}

// ─── status bar ───

export interface BalanceInfo {
  available: boolean;
  display: string;
}

export interface ContextInfo {
  used: number;
  window: number;
}

export interface Meta {
  label: string;
  cwd?: string;
  startupErr?: string;
}

export interface JobView {
  id: string;
  kind: string;
  label: string;
  status: string;
}

// ─── model / provider / settings (desktop/settings_app.go) ───

export interface ModelInfo {
  ref: string;
  model: string;
  current?: boolean;
}

export interface ProviderView {
  name: string;
  kind: string;
  baseUrl: string;
  models: string[];
  default: string;
  apiKeyEnv: string;
  keySet: boolean;
  balanceUrl?: string;
  contextWindow?: number;
}

export interface PermissionsView {
  mode: string;
  deny: string[];
  allow: string[];
  ask: string[];
}

export interface SandboxView {
  bash: string;
  network: boolean;
}

export interface PluginView {
  name: string;
  type: string;
  command: string;
  args: string;
  url: string;
  keySet: boolean;
}

export interface RouterView {
  enabled: boolean;
  cheapModel: string;
  expensiveModel: string;
}

export interface AgentParams {
  temperature: number;
  maxSteps: number;
  systemPrompt: string;
}

export interface BotStatus {
  name: string;
  keySet: boolean;
  running: boolean;
}

export interface SettingsView {
  defaultModel: string;
  plannerModel: string;
  language: string;
  providerKinds: string[];
  providers: ProviderView[];
  permissions: PermissionsView;
  sandbox: SandboxView;
  plugins: PluginView[];
  router: RouterView;
  configPath: string;
  bypass: boolean;
}

// ─── slash / commands ───

export interface CommandInfo {
  kind: string;
  name: string;
  description: string;
  hint?: string;
  insert?: string;
}

export interface SlashArgItem {
  label: string;
  hint?: string;
  insert: string;
}

export interface SlashArgsResult {
  from: number;
  items: SlashArgItem[];
}

export interface DirEntry {
  name: string;
  isDir: boolean;
}
