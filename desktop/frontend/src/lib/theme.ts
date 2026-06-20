// theme.ts manages the appearance override. The stylesheet is dark by default and
// follows the OS via prefers-color-scheme; this lets the user force a theme by
// setting data-theme on <html> ("dark" / "light"), or "auto" to remove it and
// follow the OS again. The choice persists in localStorage and is applied on load.

export type Theme = "auto" | "light" | "dark";

const KEY = "ok-theme";

export function getTheme(): Theme {
  const v = typeof localStorage !== "undefined" ? localStorage.getItem(KEY) : null;
  return v === "light" || v === "dark" ? v : "auto";
}

export function applyTheme(theme: Theme): void {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  if (theme === "auto") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);
  try {
    localStorage.setItem(KEY, theme);
  } catch {
    /* private mode / no storage — the in-DOM attribute still applies */
  }
}

// initTheme applies the saved choice once at startup (before React renders).
export function initTheme(): void {
  applyTheme(getTheme());
}
