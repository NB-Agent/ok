// Syntax highlighting via highlight.js core with a hand-picked language set
// (registering only what a coding agent surfaces keeps the bundle lean). This is
// the engine behind the editor seam's HljsCode / HljsDiff; token colors are
// themed in styles.css (.hljs-*) to match the app palette rather than a stock CSS.

import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import css from "highlight.js/lib/languages/css";
import go from "highlight.js/lib/languages/go";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import markdown from "highlight.js/lib/languages/markdown";
import python from "highlight.js/lib/languages/python";
import rust from "highlight.js/lib/languages/rust";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

import { ALIASES } from "./lang";

hljs.registerLanguage("bash", bash);
hljs.registerLanguage("css", css);
hljs.registerLanguage("go", go);
hljs.registerLanguage("javascript", javascript);
hljs.registerLanguage("json", json);
hljs.registerLanguage("markdown", markdown);
hljs.registerLanguage("python", python);
hljs.registerLanguage("rust", rust);
hljs.registerLanguage("typescript", typescript);
hljs.registerLanguage("xml", xml);
hljs.registerLanguage("yaml", yaml);

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// resolveLang maps a markdown fence tag or guessed name to a registered language,
// or "" when we can't highlight it (caller renders escaped plain text).
export function resolveLang(lang?: string): string {
  if (!lang) return "";
  const l = lang.toLowerCase();
  const resolved = ALIASES[l] ?? l;
  return hljs.getLanguage(resolved) ? resolved : "";
}

// highlightToHtml returns highlighted HTML (token <span>s) for the given code, or
// escaped plain text when the language is unknown. ignoreIllegals so partial /
// streaming snippets never throw.
// Output is sanitized to only allow <span> tags with class attributes.
export function highlightToHtml(code: string, lang?: string): string {
  const resolved = resolveLang(lang);
  if (!resolved) return escapeHtml(code);
  try {
    const raw = hljs.highlight(code, { language: resolved, ignoreIllegals: true }).value;
    // Sanitize: strip any HTML tags that aren't <span class="..."> to prevent
    // XSS via markdown/xml highlighting that might pass through raw HTML.
    return raw.replace(/<(?!\/?span\b)[^>]*>/g, "");
  } catch {
    return escapeHtml(code);
  }
}
