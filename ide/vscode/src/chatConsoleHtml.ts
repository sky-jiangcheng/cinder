// The console's HTML — the panel's markup, styles and script.
//
// It lives in its own module (no `vscode` import) for two reasons: the
// webview is the one piece of the extension no unit test can reach, and the
// preview harness (scripts/preview-console.mjs) has to render it outside the
// editor. "It compiles" is not "it renders".
/** How many recent turns stay expanded; the rest fold into one row. */
export const VISIBLE_TURNS = 2;

// The panel's own action strip (ADR-0044 decision 1).
//
// The icons are inlined instead of loaded from `media/`: the webview has no
// `localResourceRoots`, so an external file 404s — and a 404 icon shows up in
// VS Code as an empty box, which is harder to diagnose than a missing button.
// All four draw with `currentColor`, so they follow the theme by themselves.
const ICON_SEARCH = `<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><circle cx="7" cy="7" r="4.5" fill="none" stroke="currentColor" stroke-width="1.4"/><path d="M10.5 10.5 14 14" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/></svg>`;

const ICON_SETTINGS = `<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><circle cx="8" cy="8" r="2.2" fill="none" stroke="currentColor" stroke-width="1.3"/><path d="M8 1.6v1.9M8 12.5v1.9M1.6 8h1.9M12.5 8h1.9M3.5 3.5l1.3 1.3M11.2 11.2l1.3 1.3M12.5 3.5l-1.3 1.3M4.8 11.2l-1.3 1.3" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>`;

const ICON_DIAGNOSE = `<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><path d="M2 4.5h12M2 8h12M2 11.5h12" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/><circle cx="5.5" cy="4.5" r="1.8" fill="none" stroke="currentColor" stroke-width="1.3"/><circle cx="10" cy="8" r="1.8" fill="none" stroke="currentColor" stroke-width="1.3"/><circle cx="6.5" cy="11.5" r="1.8" fill="none" stroke="currentColor" stroke-width="1.3"/></svg>`;

const ICON_DESKTOP = `<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><rect x="1.8" y="2.8" width="12.4" height="8.4" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.3"/><path d="M5.5 13.5h5" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>`;

const KINDS = ["knowledge", "log", "idea", "other"];

/**
 * The brand mark, inlined so the webview needs no resource roots and no
 * image loads. Same construction as build/icon.svg (the brand SSOT): the
 * inverse seal — solid ink field, the C knocked out in paper, no frame.
 */
const MARK_SVG = `<svg viewBox="0 0 512 512" width="28" height="28" aria-hidden="true" focusable="false">
  <rect width="512" height="512" fill="#34322D"/>
  <g transform="translate(21.06 13.06) scale(0.941176)">
    <path fill="#F7F4EE" fill-rule="nonzero" d="M270.36 431.85Q235.78 431.85 208.01 420.89Q180.25 409.93 160.76 388.25Q141.28 366.57 130.56 334.18Q119.85 301.79 119.85 258.44Q119.85 215.57 130.56 182.2Q141.28 148.84 160.76 126.19Q180.25 103.54 208.01 91.85Q235.78 80.15 270.36 80.15Q317.61 80.15 348.3 99.4Q378.99 118.64 397.5 160.04L333.69 193.16Q326.87 171.73 312.5 158.82Q298.13 145.91 270.36 145.91Q237.73 145.91 218 167.1Q198.27 188.29 198.27 228.72V283.28Q198.27 323.71 218 344.9Q237.73 366.09 270.36 366.09Q297.64 366.09 313.96 351.23Q330.28 336.37 338.07 315.43L398.47 350.5Q379.47 389.47 348.54 410.66Q317.61 431.85 270.36 431.85Z"/>
  </g>
</svg>`;

export function consoleHtml(nonce: string): string {
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
<title>Cinder Chat</title><style>
  :root {
    --r-border: var(--vscode-panel-border, var(--vscode-contrastBorder, #444));
    --r-muted: var(--vscode-descriptionForeground, #888);
    --r-accent: var(--vscode-focusBorder, #06c);
  }
  * { box-sizing: border-box; }
  html, body { height: 100%; }
  body {
    margin: 0; display: flex; flex-direction: column; overflow: hidden;
    font-family: var(--vscode-font-family, sans-serif);
    font-size: var(--vscode-font-size, 13px);
    color: var(--vscode-foreground, #333);
    background: var(--vscode-sideBar-background, #fff);
  }
  button { font-family: var(--vscode-font-family, sans-serif); }
  /* ---------- header: identity + the panel's OWN action strip ---------- */
  /* The strip used to live in VS Code's view/title, where the host drew it
     at the far right of the sidebar title bar and cropped the third button
     off the panel's edge. The webview cannot draw that strip, so the actions
     moved in here (ADR-0044 decision 1). */
  header.bar {
    display: flex; align-items: center; gap: 8px; flex: none;
    padding: 6px 8px 6px 10px; border-bottom: 1px solid var(--r-border);
  }
  .mark { flex: none; display: block; border-radius: 3px; }
  .wordmark {
    flex: 1; min-width: 0; font-size: 13px; font-weight: 700; letter-spacing: 0.02em;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .actions { display: flex; align-items: center; gap: 1px; flex: none; }
  .icon-btn {
    flex: none; width: 24px; height: 24px; cursor: pointer;
    display: flex; align-items: center; justify-content: center;
    background: none; border: none; border-radius: 4px;
    color: var(--vscode-foreground, #333);
  }
  .icon-btn:hover { background: var(--vscode-toolbar-hoverBackground, rgba(128,128,128,.18)); }
  .icon-btn:focus-visible { outline: 1px solid var(--r-accent); outline-offset: -1px; }
  .icon-btn svg { width: 16px; height: 16px; display: block; }
  /* A narrow sidebar has no room for the wordmark; the icons never shrink. */
  @media (max-width: 260px) { .wordmark { display: none; } }
  /* ---------- announcement bar (ADR-0036 decision 5) ---------- */
  .announce {
    flex: none; display: flex; align-items: flex-start; gap: 8px;
    padding: 7px 10px; font-size: 12px; line-height: 1.45;
    border-bottom: 1px solid var(--r-border);
    background: var(--vscode-inputValidation-infoBackground, rgba(0,120,215,.12));
  }
  .announce[hidden] { display: none; }
  .announce.warn { background: var(--vscode-inputValidation-warningBackground, rgba(255,185,0,.14)); }
  .announce.error { background: var(--vscode-inputValidation-errorBackground, rgba(255,0,0,.12)); }
  .announce .msg { flex: 1; min-width: 0; overflow-wrap: anywhere; }
  .announce .x {
    flex: none; background: none; border: none; cursor: pointer;
    color: inherit; font-size: 14px; line-height: 1; padding: 0 2px; opacity: .7;
  }
  .announce .x:hover { opacity: 1; }
  /* ---------- the feed: the only scroll area ---------- */
  main.feed {
    flex: 1; min-height: 0; overflow-y: auto; overflow-x: hidden;
    padding: 10px 12px 18px;
  }
  .turn { margin-bottom: 16px; }
  .turn-head { display: flex; align-items: flex-start; gap: 6px; }
  .turn-head .mode {
    flex: none; font-size: 9px; font-weight: 700; letter-spacing: .06em;
    text-transform: uppercase; padding: 1px 5px; border-radius: 4px;
    border: 1px solid currentColor; color: var(--r-muted);
  }
  .turn-head .q {
    flex: 1; min-width: 0; font-size: 12px; font-weight: 600; line-height: 1.45;
    overflow-wrap: anywhere;
  }
  .turn-head .chev {
    flex: none; background: none; border: none; cursor: pointer;
    color: var(--r-muted); font-size: 12px; line-height: 1.3; padding: 0 2px;
  }
  .turn-head .chev:hover { color: var(--vscode-foreground, #333); }
  .turn-body { margin-top: 6px; padding-left: 2px; }
  .pending { font-size: 12px; color: var(--r-muted); font-style: italic; }
  .turn-error {
    font-size: 12px; line-height: 1.5; padding: 7px 9px;
    border: 1px solid var(--vscode-inputValidation-errorBorder, #c33);
    border-radius: 5px; overflow-wrap: anywhere;
    background: var(--vscode-inputValidation-errorBackground, rgba(255,0,0,.08));
  }
  .answer { font-size: 13px; line-height: 1.65; overflow-wrap: anywhere; }
  .answer pre.raw {
    margin: 0; padding: 9px 11px; white-space: pre-wrap; overflow-wrap: anywhere;
    border: 1px dashed var(--r-border); border-radius: 5px;
    font-family: var(--vscode-editor-font-family, monospace); font-size: 12px;
  }
  /* the shared markdown renderer (note bodies AND answers) */
  .answer pre, .detail-body pre {
    margin: 10px 0; padding: 10px 12px; overflow-x: auto;
    background: var(--vscode-textCodeBlock-background, rgba(128,128,128,.12));
    border-radius: 6px; font-family: var(--vscode-editor-font-family, monospace);
    font-size: 12px;
  }
  .answer code, .detail-body code {
    padding: 1px 4px; border-radius: 3px;
    background: var(--vscode-textCodeBlock-background, rgba(128,128,128,.12));
    font-family: var(--vscode-editor-font-family, monospace); font-size: 12px;
  }
  .answer pre code, .detail-body pre code { padding: 0; background: none; }
  .answer h3, .answer h4, .detail-body h3, .detail-body h4 { margin: 14px 0 6px; }
  .answer ul, .answer ol, .detail-body ul, .detail-body ol { margin: 8px 0; padding-left: 22px; }
  .answer a, .detail-body a { color: var(--vscode-textLink-foreground, #06c); }
  .diagram {
    margin: 10px 0; border: 1px solid var(--r-border); border-radius: 6px; overflow: hidden;
  }
  .diagram-head {
    padding: 4px 10px; font-size: 10px; font-weight: 600; letter-spacing: .04em;
    text-transform: uppercase; color: var(--r-muted);
    background: var(--vscode-textCodeBlock-background, rgba(128,128,128,.12));
  }
  .diagram pre { margin: 0; border-radius: 0; }
  .diagram-note {
    padding: 4px 10px; font-size: 10px; color: var(--r-muted);
    border-top: 1px solid var(--r-border);
  }
  /* ---------- sources: the footnote of an answer ---------- */
  .sources-toggle {
    margin-top: 8px; padding: 2px 0; background: none; border: none; cursor: pointer;
    font-size: 11px; color: var(--r-muted);
  }
  .sources-toggle:hover { color: var(--vscode-foreground, #333); }
  .sources {
    margin-top: 4px; padding-left: 8px; border-left: 2px solid var(--r-border);
  }
  .src-item { font-size: 11px; line-height: 1.5; margin-bottom: 5px; overflow-wrap: anywhere; }
  .src-item .ref { font-weight: 700; font-family: var(--vscode-editor-font-family, monospace); }
  .src-item .stype { font-weight: 600; }
  .src-item .smeta { color: var(--r-muted); }
  /* ---------- cards ---------- */
  .cards { display: flex; flex-direction: column; gap: 6px; }
  .card { border: 1px solid var(--r-border); border-radius: 6px; padding: 7px 9px; }
  .card:hover { border-color: var(--r-accent); }
  .card-row { display: flex; align-items: baseline; gap: 6px; cursor: pointer; }
  .badge {
    flex: none; font-size: 9px; font-weight: 600; letter-spacing: 0.04em;
    text-transform: uppercase; padding: 1px 5px; border-radius: 4px;
    border: 1px solid currentColor;
  }
  .badge.note { color: var(--vscode-textLink-foreground, #06c); }
  .badge.todo { color: var(--vscode-editorWarning-foreground, #b90); }
  .badge.wiki, .badge.page { color: var(--vscode-charts-purple, #8250df); }
  .card-title {
    flex: 1; min-width: 0; font-size: 12px; font-weight: 600; line-height: 1.4;
    overflow-wrap: anywhere;
  }
  .card-meta { flex: none; font-size: 10px; color: var(--r-muted); }
  .card-snippet {
    margin-top: 3px; font-size: 11px; line-height: 1.45; color: var(--r-muted);
    display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden;
  }
  .card-detail { margin-top: 7px; padding-top: 7px; border-top: 1px dashed var(--r-border); }
  .card-detail:empty { display: none; }
  .detail-title { font-size: 13px; font-weight: 700; margin-bottom: 4px; overflow-wrap: anywhere; }
  .detail-meta {
    display: flex; flex-wrap: wrap; align-items: center; gap: 6px;
    font-size: 10px; color: var(--r-muted); margin-bottom: 8px;
  }
  .detail-body { font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
  .card-actions { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 8px; }
  .card-actions button, .editor .actions button {
    padding: 3px 9px; font-size: 11px; cursor: pointer;
    color: var(--vscode-button-foreground, #fff);
    background: var(--vscode-button-background, #06c);
    border: none; border-radius: 4px;
  }
  .card-actions button.secondary, .editor .actions button.secondary {
    color: var(--vscode-foreground, #333);
    background: var(--vscode-button-secondaryBackground, rgba(128,128,128,.18));
  }
  .card-actions button:hover, .editor .actions button:hover { filter: brightness(1.12); }
  .card-actions button:disabled, .editor .actions button:disabled { opacity: .55; cursor: default; filter: none; }
  .scope-note { margin-top: 6px; font-size: 10px; line-height: 1.5; color: var(--r-muted); }
  /* ---------- editor (edit / new / AI proposal), inside a card ---------- */
  .editor { display: flex; flex-direction: column; gap: 7px; }
  .editor .banner {
    padding: 6px 8px; font-size: 11px; line-height: 1.5;
    border: 1px solid var(--r-accent); border-radius: 5px;
    background: rgba(0,120,215,.08);
  }
  .editor .banner em { display: block; margin-top: 3px; font-style: normal; color: var(--r-muted); }
  .editor .err {
    padding: 6px 8px; font-size: 11px;
    border: 1px solid var(--vscode-inputValidation-errorBorder, #c33); border-radius: 5px;
    background: var(--vscode-inputValidation-errorBackground, rgba(255,0,0,.08));
  }
  .editor .err[hidden] { display: none; }
  .editor input[type=text], .editor select, .editor textarea {
    width: 100%; padding: 4px 7px; font-size: 11px;
    font-family: var(--vscode-font-family, sans-serif);
    color: var(--vscode-input-foreground, #333);
    background: var(--vscode-input-background, #fff);
    border: 1px solid var(--vscode-input-border, #ccc);
    border-radius: 4px; outline: none;
  }
  .editor input:focus, .editor select:focus, .editor textarea:focus { border-color: var(--r-accent); }
  .editor textarea {
    min-height: 120px; resize: vertical; line-height: 1.5;
    font-family: var(--vscode-editor-font-family, monospace);
  }
  .editor .row { display: flex; gap: 6px; }
  .editor .row select { flex: 1; min-width: 0; }
  .editor .label { font-size: 9px; font-weight: 600; letter-spacing: .05em; text-transform: uppercase; color: var(--r-muted); }
  .editor .preview {
    padding: 8px 10px; font-size: 12px; line-height: 1.6;
    border: 1px dashed var(--r-border); border-radius: 5px; overflow-wrap: anywhere;
  }
  /* ---------- the composer (always there) ---------- */
  footer.composer {
    flex: none; position: relative;
    border-top: 1px solid var(--r-border); padding: 8px 10px 8px;
  }
  .input-wrap { position: relative; }
  .composer textarea {
    display: block; width: 100%; min-height: 32px; max-height: 190px; resize: none;
    padding: 6px 30px 6px 8px; font-size: 12px; line-height: 1.5;
    font-family: var(--vscode-font-family, sans-serif);
    color: var(--vscode-input-foreground, #333);
    background: var(--vscode-input-background, #fff);
    border: 1px solid var(--vscode-input-border, #ccc); border-radius: 5px; outline: none;
  }
  .composer textarea:focus { border-color: var(--r-accent); }
  /* The send key sits INSIDE the input box (Windsurf's shape), so the input
     grows downward and never covers it. */
  .send {
    position: absolute; right: 3px; bottom: 3px;
    width: 24px; height: 24px; padding: 0; font-size: 12px; line-height: 1; cursor: pointer;
    display: flex; align-items: center; justify-content: center;
    border: none; border-radius: 4px;
    color: var(--vscode-button-foreground, #fff);
    background: var(--vscode-button-background, #06c);
    transition: background 120ms ease, opacity 120ms ease;
  }
  .send:hover:not(:disabled) { background: var(--vscode-button-hoverBackground, var(--vscode-button-background, #06c)); filter: brightness(.94); }
  .send:active:not(:disabled) { filter: brightness(.88); }
  .send:focus-visible { outline: 1px solid var(--vscode-focusBorder, #06c); outline-offset: 1px; }
  .send:disabled { opacity: .5; cursor: default; }
  /* The tool row: the mode group on the left, the project chip on the right.
     It wraps instead of clipping: a narrow side bar used to hide the chip
     beyond overflow:hidden, which made project scope unreachable. */
  .tools { display: flex; align-items: center; flex-wrap: wrap; gap: 6px 6px; margin-top: 7px; }
  .modes { display: flex; gap: 3px; flex: none; flex-wrap: wrap; }
  .mode-btn {
    padding: 4px 8px; min-height: 24px; font-size: 11px; cursor: pointer; border-radius: 6px;
    border: 1px solid var(--r-border);
    color: var(--r-muted); background: transparent;
    transition: color 120ms ease, border-color 120ms ease;
  }
  .mode-btn:hover { color: var(--vscode-foreground, #333); }
  .mode-btn:active { color: var(--vscode-foreground, #333); border-color: var(--vscode-foreground, #333); }
  .mode-btn:focus-visible { outline: 1px solid var(--vscode-focusBorder, #06c); outline-offset: 1px; }
  .mode-btn.active {
    color: var(--vscode-button-foreground, #fff);
    background: var(--vscode-button-background, #06c); border-color: transparent;
  }
  .chip {
    margin-left: auto; flex: none; max-width: 100%; min-height: 24px; overflow: hidden;
    text-overflow: ellipsis; white-space: nowrap;
    padding: 4px 9px; font-size: 11px; cursor: pointer; border-radius: 6px;
    border: 1px solid var(--r-border); background: transparent; color: var(--r-muted);
    transition: color 120ms ease, border-color 120ms ease;
  }
  .chip:active { color: var(--vscode-foreground, #333); }
  .chip:focus-visible { outline: 1px solid var(--vscode-focusBorder, #06c); outline-offset: 1px; }
  .chip.locked { color: var(--vscode-foreground, #333); border-color: var(--r-accent); }
  .chip.pick { color: var(--vscode-foreground, #333); border-style: dashed; }
  /* ---------- the project picker (a popover under the chip) ---------- */
  /* Anchored to the composer's top edge, not a magic pixel offset: the input
     grows with the question, and a fixed bottom: 46px used to end up underneath the
     textarea and the send key. */
  .pop {
    position: absolute; z-index: 5; left: 10px; right: 10px; bottom: 100%;
    margin-bottom: 6px;
    max-height: 220px; overflow-y: auto;
    border: 1px solid var(--r-border); border-radius: 6px;
    background: var(--vscode-dropdown-background, var(--vscode-editorWidget-background, #fff));
    box-shadow: 0 4px 14px rgba(0,0,0,.22);
  }
  .pop[hidden] { display: none; }
  .pop-item {
    display: block; width: 100%; text-align: left;
    padding: 6px 10px; font-size: 12px; cursor: pointer;
    background: none; border: none; color: var(--vscode-foreground, #333);
  }
  .pop-item:hover { background: var(--vscode-list-hoverBackground, rgba(128,128,128,.18)); }
  .pop-empty { padding: 8px 10px; font-size: 11px; color: var(--r-muted); }
  /* ---------- the provider sheet (ADR-0044 decision 5) ---------- */
  .sheet {
    position: absolute; inset: 0; z-index: 10;
    display: flex; align-items: flex-start; justify-content: center;
    padding: 24px 12px; overflow-y: auto;
    background: rgba(0,0,0,.32);
  }
  .sheet[hidden] { display: none; }
  /* The inputs below set display: block, which would beat the UA's
     display: none for [hidden] — the model picker would show up empty. */
  .sheet [hidden] { display: none; }
  .sheet-panel {
    width: 100%; max-width: 360px; padding: 12px 14px 14px;
    border: 1px solid var(--r-border); border-radius: 8px;
    background: var(--vscode-editorWidget-background, var(--vscode-editor-background, #fff));
  }
  .sheet-head { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
  .sheet-head b { flex: 1; font-size: 13px; }
  .sheet-head .x {
    flex: none; background: none; border: none; cursor: pointer; color: inherit;
    font-size: 14px; line-height: 1; padding: 0 2px; opacity: .7;
  }
  .sheet-head .x:hover { opacity: 1; }
  .sheet-note { margin: 0 0 10px; font-size: 11px; line-height: 1.5; color: var(--r-muted); }
  .sheet label {
    display: block; margin-bottom: 8px; font-size: 10px; font-weight: 600;
    letter-spacing: .05em; text-transform: uppercase; color: var(--r-muted);
  }
  .sheet input[type=text], .sheet input[type=password], .sheet select {
    display: block; width: 100%; margin-top: 3px; padding: 4px 7px; font-size: 12px;
    font-family: var(--vscode-font-family, sans-serif);
    color: var(--vscode-input-foreground, #333);
    background: var(--vscode-input-background, #fff);
    border: 1px solid var(--vscode-input-border, #ccc); border-radius: 4px; outline: none;
  }
  .sheet input:focus, .sheet select:focus { border-color: var(--r-accent); }
  .sheet-row { display: flex; gap: 6px; margin-top: 4px; }
  .sheet-row button {
    flex: 1; padding: 5px 8px; font-size: 11px; cursor: pointer;
    border: 1px solid var(--r-border); border-radius: 4px;
    color: var(--vscode-foreground, #333);
    background: var(--vscode-button-secondaryBackground, rgba(128,128,128,.15));
  }
  .sheet-row button.primary {
    color: var(--vscode-button-foreground, #fff);
    background: var(--vscode-button-background, #06c); border-color: transparent;
  }
  .sheet-row button:disabled { opacity: .55; cursor: default; }
  .sheet-status { margin-top: 9px; font-size: 11px; line-height: 1.5; color: var(--r-muted); overflow-wrap: anywhere; }
  .sheet-status.error { color: var(--vscode-errorForeground, #c33); }
  .sheet-status.info { color: var(--vscode-foreground, #333); }
  .shared-topics { padding: 8px 12px; border-bottom: 1px solid var(--r-border); }
  .topic-row { display: flex; gap: 6px; align-items: center; min-width: 0; }
  .topic-row[hidden] { display: none; }
  .topic-row select, .topic-row input { flex: 1; min-width: 0; width: 0; padding: 6px 8px; border: 1px solid var(--r-border); border-radius: 8px; background: var(--vscode-input-background, transparent); color: var(--vscode-foreground, inherit); font: inherit; }
  .topic-row button { cursor: pointer; color: var(--vscode-foreground, inherit); background: transparent; border: 1px solid var(--r-border); border-radius: 6px; padding: 5px; }
  #topic-form { margin-top: 8px; }
  #topic-status { margin: 6px 0 0; font-size: 11px; line-height: 1.5; color: var(--r-muted); overflow-wrap: anywhere; }
  #topic-status.error { color: var(--vscode-errorForeground, #c33); }
  button:focus-visible, select:focus-visible, input:focus-visible, textarea:focus-visible { outline: 2px solid var(--vscode-focusBorder, currentColor); outline-offset: 2px; }
  /* ---------- the empty state ---------- */
  .empty { padding: 18px 12px; font-size: 12px; line-height: 1.6; color: var(--r-muted); }
  .empty h2 { margin: 0 0 8px; font-size: 14px; font-weight: 700; color: var(--vscode-foreground, #333); }
  .empty p { margin: 0 0 8px; }
  .empty .mark { width: 44px; height: 44px; margin-bottom: 12px; }
  .empty kbd {
    padding: 1px 5px; font-size: 10px; border-radius: 3px;
    font-family: var(--vscode-editor-font-family, monospace);
    border: 1px solid var(--r-border);
  }
  .examples { list-style: none; margin: 0; padding: 0; }
  .examples li { margin-bottom: 5px; }
  .examples button {
    width: 100%; text-align: left; padding: 6px 9px; font-size: 12px; cursor: pointer;
    border: 1px solid var(--r-border); border-radius: 5px;
    color: var(--vscode-foreground, #333); background: transparent;
  }
  .examples button:hover { border-color: var(--r-accent); }
</style>
</head>
<body>
<header class="bar">
  <span class="mark">${MARK_SVG}</span>
  <span class="wordmark">Cinder</span>
  <span class="actions">
    <button class="icon-btn" id="act-search" type="button" title="Search notes &amp; todos" aria-label="Search notes and todos">${ICON_SEARCH}</button>
    <button class="icon-btn" id="act-settings" type="button" title="AI provider" aria-label="AI provider">${ICON_SETTINGS}</button>
    <button class="icon-btn" id="act-diagnose" type="button" title="Setup &amp; diagnostics" aria-label="Setup and diagnostics">${ICON_DIAGNOSE}</button>
    <button class="icon-btn" id="act-desktop" type="button" title="Open the Cinder Desktop app" aria-label="Open the Cinder Desktop app">${ICON_DESKTOP}</button>
  </span>
</header>
<section class="shared-topics" aria-label="Shared conversation topics">
  <div class="topic-row">
    <select id="topic-select" aria-label="Conversation topic"><option value="">Local knowledge console</option></select>
    <button class="icon-btn" id="topic-new" type="button" aria-label="New shared conversation" title="New shared conversation">+</button>
    <button class="icon-btn" id="topic-refresh" type="button" aria-label="Refresh shared conversations" title="Refresh shared conversations">↻</button>
  </div>
  <form id="topic-form" class="topic-row" hidden>
    <input id="topic-title" aria-label="Conversation title" placeholder="Name this conversation" maxlength="160" required>
    <button type="submit">Create</button>
    <button id="topic-cancel" type="button">Cancel</button>
  </form>
  <p id="topic-status" role="status">Shared through Cinder · connect Cinder MCP to sync</p>
</section>
<div class="announce" id="announce" hidden>
  <span class="msg" id="announce-msg"></span>
  <button class="x" id="announce-x" type="button" title="Dismiss" aria-label="Dismiss">✕</button>
</div>
<main class="feed" id="feed"></main>
<footer class="composer">
  <div class="pop" id="project-pop" hidden></div>
  <div class="input-wrap">
    <textarea id="q" rows="1" placeholder="Ask about this project…" spellcheck="false"></textarea>
    <button class="send" id="send" type="button" title="Send (Enter)" aria-label="Send">↵</button>
  </div>
  <div class="tools">
    <span class="modes" role="group" aria-label="Knowledge mode">
      <button class="mode-btn active" data-mode="ask" type="button">Ask</button>
      <button class="mode-btn" data-mode="notes" type="button">Notes</button>
      <button class="mode-btn" data-mode="pages" type="button">Pages</button>
    </span>
    <button class="chip" id="chip" type="button">All projects</button>
  </div>
</footer>
<div class="sheet" id="sheet" hidden>
  <div class="sheet-panel">
    <div class="sheet-head">
      <b>AI provider</b>
      <button class="x" id="sheet-x" type="button" title="Close" aria-label="Close">✕</button>
    </div>
    <p class="sheet-note">Cinder keeps these in its local service. The desktop app and this panel use the same ones.</p>
    <label>Base URL<input id="ai-base" type="text" spellcheck="false" placeholder="http://127.0.0.1:1234/v1"></label>
    <label>API key<input id="ai-key" type="password" spellcheck="false" placeholder="leave empty to keep the stored key"></label>
    <label>Model<input id="ai-model" type="text" spellcheck="false" placeholder="the model id to ask"></label>
    <select id="ai-picker" hidden></select>
    <div class="sheet-row">
      <button id="ai-models" type="button">List models</button>
      <button id="ai-test" type="button">Test</button>
      <button id="ai-save" class="primary" type="button">Save</button>
    </div>
    <div class="sheet-status" id="ai-status"></div>
  </div>
</div>
<script nonce="${nonce}">
const MARK_SVG = ${JSON.stringify(MARK_SVG)};
const KINDS = ${JSON.stringify(KINDS)};
const VISIBLE_TURNS = ${VISIBLE_TURNS};
const vscode = acquireVsCodeApi();
const feedEl = document.getElementById('feed');
const announceEl = document.getElementById('announce');
const announceMsg = document.getElementById('announce-msg');
const q = document.getElementById('q');
const sendBtn = document.getElementById('send');
const chip = document.getElementById('chip');
const modeBtns = Array.from(document.querySelectorAll('.mode-btn'));
const popEl = document.getElementById('project-pop');
const sheetEl = document.getElementById('sheet');
const aiBase = document.getElementById('ai-base');
const aiKey = document.getElementById('ai-key');
const aiModel = document.getElementById('ai-model');
const aiPicker = document.getElementById('ai-picker');
const aiStatus = document.getElementById('ai-status');

// ---- state -----------------------------------------------------------------
// The host owns the DATA (the turns); this side owns the FOLD STATE. A
// collapsed turn must stay collapsed while other turns arrive.
let mode = 'ask';
let scope = { projectId: 0, projectName: '', repository: '', locked: false };
let turns = [];
let sharedThreadId = null;
let sharedBusy = false;
let sharedSignature = '';
const drafts = new Map();
const topicSelect = document.getElementById('topic-select');
const topicStatus = document.getElementById('topic-status');
let topicSignature = '';
let collapsedTurns = new Set();
let expandedSources = new Set();
let expandedCards = new Set();
let showEarlier = false;
let announcement = null;
// The projects the workspace could belong to (ADR-0044 decision 6). The host
// sends them when the workspace resolves to none.
let candidates = [];
// The open editor: { turnId, cardId, node }. The node is kept alive across
// re-renders so a half-typed draft survives an unrelated turn update.
let editor = null;

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => (
  { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
));

// Minimal markdown: the note store is markdown, and a wall of raw asterisks
// undoes the point of a reader. Anything unknown is escaped and shown
// verbatim — never dropped.
function md(src) {
  const blocks = [];
  let text = String(src ?? '');
  text = text.replace(/\`\`\`([^\\n]*)\\n([\\s\\S]*?)\`\`\`/g, (_, lang, code) => {
    if (lang.trim().toLowerCase() === 'mermaid') {
      blocks.push('<div class="diagram"><div class="diagram-head">Flowchart · mermaid source</div>' +
        '<pre>' + esc(code.replace(/^\\n/, '')) + '</pre>' +
        '<div class="diagram-note">Renders as a diagram in the Cinder Desktop app; the source stays editable here.</div></div>');
    } else {
      blocks.push('<pre><code>' + esc((lang ? lang + '\\n' : '') + code.replace(/^\\n/, '')) + '</code></pre>');
    }
    return '\\u0000' + (blocks.length - 1) + '\\u0000';
  });
  const inline = (s) => esc(s)
    .replace(/\`([^\`\\n]+)\`/g, '<code>$1</code>')
    .replace(/\\*\\*([^*\\n]+)\\*\\*/g, '<strong>$1</strong>')
    .replace(/\\[([^\\]]+)\\]\\(([^)\\s]+)\\)/g, '<a href="$2">$1</a>');
  const out = [];
  let list = null;
  for (const para of text.split(/\\n{2,}/)) {
    const p = para.trim();
    if (!p) continue;
    if (/^\\u0000\\d+\\u0000$/.test(p)) { out.push(blocks[+p.slice(1, -1)]); list = null; continue; }
    const lines = p.split('\\n');
    let buffer = [];
    const flush = () => { if (buffer.length) { out.push('<p>' + buffer.map(inline).join('<br>') + '</p>'); buffer = []; } };
    for (const line of lines) {
      const h = line.match(/^(#{1,4})\\s+(.*)$/);
      const ul = line.match(/^[-*]\\s+(.*)$/);
      const ol = line.match(/^\\d+[.)]\\s+(.*)$/);
      if (h) { flush(); out.push('<h' + (h[1].length + 2) + '>' + inline(h[2]) + '</h' + (h[1].length + 2) + '>'); }
      else if (ul) { flush(); if (list !== 'ul') { if (list) out.push('</' + list + '>'); out.push('<ul>'); list = 'ul'; } out.push('<li>' + inline(ul[1]) + '</li>'); }
      else if (ol) { flush(); if (list !== 'ol') { if (list) out.push('</' + list + '>'); out.push('<ol>'); list = 'ol'; } out.push('<li>' + inline(ol[1]) + '</li>'); }
      else { if (list) { out.push('</' + list + '>'); list = null; } buffer.push(line); }
    }
    flush();
    if (list) { out.push('</' + list + '>'); list = null; }
  }
  return out.join('\\n');
}

function badge(text, cls) {
  return '<span class="badge ' + esc(cls || text) + '">' + esc(text) + '</span>';
}

function cardBadge(card) {
  if (card.type === 'wiki') return badge(card.pageKind || 'wiki', 'wiki');
  return badge(card.type, card.type);
}

// ---- scope chip ------------------------------------------------------------
// The chip is never disabled. An unresolvable workspace used to disable it,
// which left the reader with nothing to do (ADR-0044 decision 6); now it
// opens the project picker instead.
function renderScope() {
  if (!scope.projectId) {
    chip.textContent = candidates.length ? 'Choose project' : 'All projects';
    chip.className = 'chip pick';
    chip.title = 'This workspace does not resolve to a Cinder project yet — click to pick one.';
    return;
  }
  if (scope.locked) {
    chip.textContent = '🔒 ' + scope.projectName;
    chip.className = 'chip locked';
    chip.title = 'Locked to ' + scope.projectName + ' — click to search across all projects.';
  } else {
    chip.textContent = 'All projects';
    chip.className = 'chip';
    chip.title = 'Searching across all projects — click to lock back to ' + scope.projectName + '.';
  }
}

// ---- the project picker -----------------------------------------------------
function closePop() {
  popEl.hidden = true;
  popEl.innerHTML = '';
}

function renderPop() {
  popEl.innerHTML = '';
  const all = document.createElement('button');
  all.className = 'pop-item';
  all.type = 'button';
  all.textContent = 'All projects';
  all.addEventListener('click', () => {
    closePop();
    if (scope.projectId) vscode.postMessage({ type: 'scopeToggle' });
  });
  popEl.appendChild(all);
  for (const c of candidates) {
    const item = document.createElement('button');
    item.className = 'pop-item';
    item.type = 'button';
    item.textContent = c.name;
    item.addEventListener('click', () => {
      closePop();
      vscode.postMessage({ type: 'pickProject', projectId: c.id });
    });
    popEl.appendChild(item);
  }
  if (!candidates.length) {
    const empty = document.createElement('div');
    empty.className = 'pop-empty';
    empty.textContent = 'No projects in the knowledge base yet — run a scan in the desktop app.';
    popEl.appendChild(empty);
  }
  popEl.hidden = false;
}

// ---- announcement bar ------------------------------------------------------
function renderAnnounce() {
  if (!announcement) { announceEl.hidden = true; return; }
  announceEl.hidden = false;
  announceEl.className = 'announce ' + announcement.level;
  announceMsg.textContent = announcement.message;
}

// ---- the feed --------------------------------------------------------------
function renderFeed() {
  const stick = feedEl.scrollHeight - feedEl.scrollTop - feedEl.clientHeight < 48;
  const top = feedEl.scrollTop;
  feedEl.innerHTML = '';
  if (!turns.length) {
    const empty = document.createElement('div');
    empty.className = 'empty';
    // Copy only talks about actions (ADR-0044 decision 4): one question and
    // three tappable examples. The old four-paragraph pitch described the
    // product instead of telling the reader what to do.
    empty.innerHTML = MARK_SVG +
      '<h2>Ask what your project knows.</h2>' +
      '<p>Cinder answers from the notes your coding agents left behind, with the sources listed.</p>' +
      '<ul class="examples">' +
      '<li><button type="button" data-example="ask">Why was the retry budget raised?</button></li>' +
      '<li><button type="button" data-example="notes">Search the notes &amp; todos</button></li>' +
      '<li><button type="button" data-example="pages">Browse the approved knowledge pages</button></li>' +
      '</ul>';
    for (const btn of Array.from(empty.querySelectorAll('[data-example]'))) {
      btn.addEventListener('click', () => {
        const kind = btn.getAttribute('data-example');
        if (kind === 'notes' || kind === 'pages') {
          setMode(kind);
          vscode.postMessage({ type: 'mode', mode: kind });
          q.focus();
          return;
        }
        q.focus();
      });
    }
    feedEl.appendChild(empty);
    return;
  }
  const hidden = Math.max(0, turns.length - VISIBLE_TURNS);
  if (hidden > 0) {
    const fold = document.createElement('button');
    fold.className = 'earlier';
    fold.type = 'button';
    fold.textContent = (showEarlier ? '▾ ' : '▸ ') + hidden + ' earlier turn' + (hidden === 1 ? '' : 's');
    fold.title = showEarlier ? 'Collapse the earlier turns' : 'Show the earlier turns';
    fold.addEventListener('click', () => { showEarlier = !showEarlier; renderFeed(); });
    feedEl.appendChild(fold);
  }
  const visible = showEarlier ? turns : turns.slice(-VISIBLE_TURNS);
  for (const turn of visible) feedEl.appendChild(renderTurn(turn));
  // The feed is rebuilt from scratch, so the scroll position has to be put
  // back: a reader who scrolled up to re-read an old answer must not be
  // yanked to the bottom by the next turn arriving.
  feedEl.scrollTop = stick ? feedEl.scrollHeight : top;
}

function renderTurn(turn) {
  const el = document.createElement('article');
  el.className = 'turn';
  el.dataset.id = turn.id;
  const collapsed = collapsedTurns.has(turn.id);
  const head = document.createElement('div');
  head.className = 'turn-head';
  const modeLabel = turn.mode === 'ask' ? 'Ask' : (turn.mode === 'notes' ? 'Notes' : 'Pages');
  head.innerHTML = '<span class="mode">' + esc(modeLabel) + '</span>' +
    '<span class="q">' + esc(turn.query) + '</span>';
  const chev = document.createElement('button');
  chev.className = 'chev';
  chev.type = 'button';
  chev.textContent = collapsed ? '▸' : '▾';
  chev.title = collapsed ? 'Expand this turn' : 'Collapse this turn';
  chev.addEventListener('click', () => {
    if (collapsedTurns.has(turn.id)) collapsedTurns.delete(turn.id);
    else collapsedTurns.add(turn.id);
    renderFeed();
  });
  head.appendChild(chev);
  el.appendChild(head);
  const body = document.createElement('div');
  body.className = 'turn-body';
  if (!collapsed) body.appendChild(renderTurnBody(turn));
  el.appendChild(body);
  return el;
}

function renderTurnBody(turn) {
  const frag = document.createDocumentFragment();
  if (turn.state === 'pending') {
    const p = document.createElement('div');
    p.className = 'pending';
    p.textContent = 'Working…';
    frag.appendChild(p);
    return frag;
  }
  if (turn.state === 'error') {
    const e = document.createElement('div');
    e.className = 'turn-error';
    e.textContent = turn.error || 'The request failed.';
    frag.appendChild(e);
    return frag;
  }
  if (turn.mode === 'ask') {
    if (turn.verbatim) {
      // The source list could not be parsed: show the tool's text as it came,
      // so nothing is swallowed.
      const pre = document.createElement('pre');
      pre.className = 'raw';
      pre.textContent = turn.answer || '';
      frag.appendChild(pre);
    } else {
      const a = document.createElement('div');
      a.className = 'answer';
      a.innerHTML = md(turn.answer || '');
      frag.appendChild(a);
    }
    const sources = turn.sources || [];
    if (sources.length) {
      const isOpen = expandedSources.has(turn.id);
      const btn = document.createElement('button');
      btn.className = 'sources-toggle';
      btn.type = 'button';
      btn.textContent = 'Sources (' + sources.length + ') ' + (isOpen ? '▾' : '▸');
      btn.addEventListener('click', () => {
        if (isOpen) expandedSources.delete(turn.id);
        else expandedSources.add(turn.id);
        renderFeed();
      });
      frag.appendChild(btn);
      if (isOpen) {
        const list = document.createElement('div');
        list.className = 'sources';
        for (const s of sources) {
          const item = document.createElement('div');
          item.className = 'src-item';
          const meta = [];
          meta.push('id ' + s.id);
          if (s.slug) meta.push('slug ' + s.slug);
          if (s.project) meta.push('project ' + s.project);
          item.innerHTML = '<span class="ref">' + esc('[' + s.ref + ']') + '</span> ' +
            '<span class="stype">' + esc(s.type) + '</span> ' +
            '<span class="title">' + esc(s.title) + '</span> ' +
            '<span class="smeta">(' + esc(meta.join(', ')) + ')</span>';
          list.appendChild(item);
        }
        frag.appendChild(list);
      }
    }
    return frag;
  }
  const cards = turn.cards || [];
  const wrap = document.createElement('div');
  wrap.className = 'cards';
  for (const card of cards) wrap.appendChild(renderCard(turn, card));
  frag.appendChild(wrap);
  if (turn.scopeNote) {
    const note = document.createElement('div');
    note.className = 'scope-note';
    note.textContent = turn.scopeNote;
    frag.appendChild(note);
  }
  return frag;
}

function renderCard(turn, card) {
  const el = document.createElement('div');
  el.className = 'card';
  el.dataset.cardId = String(card.id);
  const row = document.createElement('div');
  row.className = 'card-row';
  row.innerHTML = cardBadge(card) + '<span class="card-title">' + esc(card.title) + '</span>';
  const meta = document.createElement('span');
  meta.className = 'card-meta';
  meta.textContent = '#' + card.id;
  row.appendChild(meta);
  el.appendChild(row);
  if (card.snippet) {
    const sn = document.createElement('div');
    sn.className = 'card-snippet';
    sn.textContent = card.snippet;
    el.appendChild(sn);
  }
  const key = turn.id + '/' + card.type + '/' + card.id;
  const isOpen = expandedCards.has(key);
  const detail = document.createElement('div');
  detail.className = 'card-detail';
  if (isOpen) {
    if (editor && editor.turnId === turn.id && editor.cardType === card.type && editor.cardId === card.id) {
      detail.appendChild(editor.node);
    } else if (card.detail) {
      detail.appendChild(renderDetail(card.detail));
    } else {
      const wait = document.createElement('div');
      wait.className = 'pending';
      wait.textContent = 'Loading…';
      detail.appendChild(wait);
    }
  }
  el.appendChild(detail);
  if (isOpen) el.appendChild(renderCardActions(turn, card));
  row.addEventListener('click', () => {
    if (isOpen) expandedCards.delete(key);
    else {
      expandedCards.add(key);
      vscode.postMessage({ type: 'expandCard', turnId: turn.id, cardType: card.type, cardId: card.id });
    }
    renderFeed();
  });
  return el;
}

function renderDetail(d) {
  const frag = document.createDocumentFragment();
  if (d.source === 'error') {
    // The body could not be read: say so instead of showing an empty pane.
    const e = document.createElement('div');
    e.className = 'turn-error';
    e.textContent = d.body || 'This item could not be read.';
    frag.appendChild(e);
    return frag;
  }
  const title = document.createElement('div');
  title.className = 'detail-title';
  title.textContent = d.title;
  frag.appendChild(title);
  const metaParts = [];
  if (d.type === 'wiki') {
    if (d.slug) metaParts.push(d.slug);
    metaParts.push(d.project_id ? 'project ' + d.project_id : 'global');
  } else if (d.project_id) {
    metaParts.push('project ' + d.project_id);
  }
  if (d.updated_at) metaParts.push('updated ' + d.updated_at.slice(0, 10));
  if (d.tags) metaParts.push(d.tags);
  if (d.source === 'snippet') metaParts.push('excerpt');
  const meta = document.createElement('div');
  meta.className = 'detail-meta';
  meta.innerHTML = cardBadge({ type: d.type, pageKind: d.pageKind }) + '<span>·</span> #' + d.id +
    metaParts.map((m) => '<span>·</span> ' + esc(m)).join('');
  frag.appendChild(meta);
  const body = document.createElement('div');
  body.className = 'detail-body';
  body.innerHTML = d.body ? md(d.body) : '<i>No content returned for this item.</i>';
  frag.appendChild(body);
  return frag;
}

function renderCardActions(turn, card) {
  const bar = document.createElement('div');
  bar.className = 'card-actions';
  const mk = (label, cls, fn) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = cls || '';
    b.textContent = label;
    b.addEventListener('click', (ev) => { ev.stopPropagation(); fn(b); });
    return b;
  };
  bar.appendChild(mk('Open in Cinder', '', () => {
    vscode.postMessage({ type: 'open', turnId: turn.id, card });
  }));
  bar.appendChild(mk('Copy', 'secondary', () => {
    const body = (card.detail && card.detail.body) || card.snippet || '';
    vscode.postMessage({ type: 'copy', text: card.title + '\\n\\n' + body });
  }));
  if (card.type === 'note') {
    bar.appendChild(mk('Edit', 'secondary', () => {
      vscode.postMessage({ type: 'edit', turnId: turn.id, cardType: card.type, cardId: card.id });
    }));
    bar.appendChild(mk('New note here', 'secondary', () => {
      vscode.postMessage({ type: 'create', turnId: turn.id, cardType: card.type, cardId: card.id });
    }));
    bar.appendChild(mk('AI Tidy', 'secondary', (b) => {
      b.disabled = true;
      b.textContent = 'Tidying…';
      vscode.postMessage({ type: 'organize', turnId: turn.id, cardType: card.type, cardId: card.id });
    }));
  }
  return bar;
}

// ---- the editor (inside the card it belongs to) ---------------------------
function kindOptions(selected) {
  return KINDS.map((k) => '<option value="' + k + '"' + (k === selected ? ' selected' : '') + '>' + k + '</option>').join('');
}

function projectOptions(projects, selectedId) {
  return projects.map((p) =>
    '<option value="' + p.id + '"' + (p.id === selectedId ? ' selected' : '') + '>' + esc(p.name) + '</option>').join('');
}

function renderEditor(turnId, cardType, cardId, draft, projects, proposal, warning) {
  const node = document.createElement('div');
  node.className = 'editor';
  const saveLabel = draft.id ? 'Save' : 'Create note';
  const banner = proposal
    ? '<div class="banner">AI tidied draft — review, then save.' +
      (warning ? '<em>' + esc(warning) + '</em>' : '') + '</div>'
    : '';
  const projectField = projects.length
    ? '<span class="label">Project</span><select id="e-project">' + projectOptions(projects, draft.project_id) + '</select>'
    : '<span class="label">Project</span><input type="text" value="' + esc('project ' + draft.project_id) + '" disabled>';
  node.innerHTML = banner +
    '<div class="err" id="e-err" hidden></div>' +
    '<span class="label">Title</span>' +
    '<input type="text" id="e-title" value="' + esc(draft.title) + '" placeholder="note title">' +
    '<div class="row">' +
    '<div style="flex:1"><span class="label">Kind</span><select id="e-kind">' + kindOptions(draft.kind) + '</select></div>' +
    '<div style="flex:2"><span class="label">Tags</span><input type="text" id="e-tags" value="' + esc(draft.tags) + '" placeholder="comma, separated"></div>' +
    '</div>' +
    projectField +
    '<span class="label">Content (Markdown)</span>' +
    '<textarea id="e-content" spellcheck="false">' + esc(draft.content) + '</textarea>' +
    '<div class="preview"><span class="label">Preview</span><div id="e-preview-body">' +
    (draft.content ? md(draft.content) : '<i>Nothing to preview yet.</i>') + '</div></div>' +
    '<div class="actions">' +
    '<button id="e-save">' + esc(saveLabel) + '</button>' +
    '<button id="e-cancel" class="secondary">Cancel</button>' +
    '</div>';

  const content = node.querySelector('#e-content');
  const preview = node.querySelector('#e-preview-body');
  content.addEventListener('input', () => {
    preview.innerHTML = content.value ? md(content.value) : '<i>Nothing to preview yet.</i>';
  });
  node.querySelector('#e-save').addEventListener('click', () => {
    const btn = node.querySelector('#e-save');
    btn.disabled = true;
    btn.textContent = 'Saving…';
    const projectSel = node.querySelector('#e-project');
    vscode.postMessage({
      type: 'save',
      turnId,
      cardType,
      cardId,
      draft: {
        id: draft.id,
        project_id: projectSel ? Number(projectSel.value) : draft.project_id,
        title: node.querySelector('#e-title').value,
        tags: node.querySelector('#e-tags').value,
        kind: node.querySelector('#e-kind').value,
        content: content.value,
      },
    });
  });
  node.querySelector('#e-cancel').addEventListener('click', () => {
    editor = null;
    expandedCards.delete(turnId + '/' + cardType + '/' + cardId);
    vscode.postMessage({ type: 'cancelEdit', turnId, cardType, cardId });
    renderFeed();
  });

  editor = { turnId, cardType, cardId, node, saveLabel };
  expandedCards.add(turnId + '/' + cardType + '/' + cardId);
  renderFeed();
  const title = node.querySelector('#e-title');
  if (title) title.focus();
}

function showEditorError(message) {
  if (!editor) return;
  const err = editor.node.querySelector('#e-err');
  if (err) { err.textContent = message; err.hidden = false; }
  const btn = editor.node.querySelector('#e-save');
  // The draft is kept: the user fixes the transport problem and presses save
  // again, without retyping.
  if (btn) { btn.disabled = false; btn.textContent = editor.saveLabel; }
}

// ---- composer --------------------------------------------------------------
function setMode(next) {
  mode = next;
  for (const b of modeBtns) b.classList.toggle('active', b.dataset.mode === next);
  q.placeholder = next === 'ask'
    ? 'Ask about this project…'
    : (next === 'notes' ? 'Search notes & todos…' : 'Search approved knowledge pages…');
}

function submit() {
  const text = q.value.trim();
  if (!text || (sharedThreadId && sharedBusy)) return;
  if (sharedThreadId) {
    sharedBusy = true;
    sendBtn.disabled = true;
    drafts.set(sharedThreadId, q.value);
  }
  vscode.postMessage({ type: 'submit', query: text, mode: sharedThreadId ? 'ask' : mode });
  if (!sharedThreadId) { q.value = ''; q.style.height = 'auto'; }
}

for (const b of modeBtns) {
  b.addEventListener('click', () => { setMode(b.dataset.mode); vscode.postMessage({ type: 'mode', mode }); });
}

// The chip opens the project picker when the workspace resolves to nothing,
// and toggles the lock when it does (ADR-0044 decision 6).
chip.addEventListener('click', () => {
  if (!scope.projectId) {
    if (popEl.hidden) renderPop();
    else closePop();
    return;
  }
  vscode.postMessage({ type: 'scopeToggle' });
});
sendBtn.addEventListener('click', submit);

// ---- the panel's own action strip -----------------------------------------
document.getElementById('act-search').addEventListener('click', () => {
  setMode('notes');
  vscode.postMessage({ type: 'mode', mode: 'notes' });
  q.focus();
});
document.getElementById('act-settings').addEventListener('click', () => {
  sheetEl.hidden = false;
  aiStatus.textContent = '';
  aiStatus.className = 'sheet-status';
  vscode.postMessage({ type: 'openSettings' });
});
document.getElementById('act-diagnose').addEventListener('click', () => {
  vscode.postMessage({ type: 'openDiagnostics' });
});
document.getElementById('act-desktop').addEventListener('click', () => {
  vscode.postMessage({ type: 'openDesktop' });
});

// ---- the provider sheet ----------------------------------------------------
document.getElementById('sheet-x').addEventListener('click', () => {
  sheetEl.hidden = true;
  vscode.postMessage({ type: 'closeSettings' });
});
document.getElementById('ai-save').addEventListener('click', () => {
  vscode.postMessage({
    type: 'saveAISettings',
    baseUrl: aiBase.value,
    model: aiModel.value,
    apiKey: aiKey.value,
  });
});
document.getElementById('ai-test').addEventListener('click', () => {
  vscode.postMessage({
    type: 'testAIChat',
    baseUrl: aiBase.value,
    model: aiModel.value,
    apiKey: aiKey.value,
  });
});
document.getElementById('ai-models').addEventListener('click', () => {
  vscode.postMessage({ type: 'listAIModels', baseUrl: aiBase.value, apiKey: aiKey.value });
});
aiPicker.addEventListener('change', () => {
  if (aiPicker.value) aiModel.value = aiPicker.value;
});

document.getElementById('announce-x').addEventListener('click', () => {
  announcement = null;
  renderAnnounce();
});

q.addEventListener('input', () => {
  q.style.height = 'auto';
  q.style.height = Math.min(q.scrollHeight, 190) + 'px';
});
q.addEventListener('keydown', (ev) => {
  if (ev.key === 'Enter' && !ev.shiftKey && !ev.isComposing && ev.keyCode !== 229) {
    ev.preventDefault();
    submit();
  }
});

topicSelect.addEventListener('change', () => {
  drafts.set(sharedThreadId || '', q.value);
  vscode.postMessage({ type: 'sharedSelect', threadId: topicSelect.value || null });
});
document.getElementById('topic-new').addEventListener('click', () => {
  document.getElementById('topic-form').hidden = false;
  document.getElementById('topic-title').focus();
});
document.getElementById('topic-cancel').addEventListener('click', () => {
  document.getElementById('topic-form').hidden = true;
  document.getElementById('topic-new').focus();
});
document.getElementById('topic-form').addEventListener('submit', (event) => {
  event.preventDefault();
  const title = document.getElementById('topic-title').value.trim();
  if (title && !sharedBusy) vscode.postMessage({ type: 'sharedCreate', title });
});
document.getElementById('topic-refresh').addEventListener('click', () => vscode.postMessage({ type: 'sharedRefresh' }));

// ---- messages --------------------------------------------------------------
window.addEventListener('message', (event) => {
  const msg = event.data;
  if (msg.type === 'sharedSent') {
    if ((drafts.get(msg.threadId) || '').trim() === msg.query) drafts.delete(msg.threadId);
    if (sharedThreadId === msg.threadId && q.value.trim() === msg.query) {
      q.value = '';
      q.style.height = 'auto';
    }
  } else if (msg.type === 'sharedState') {
    const nextId = msg.threadId || null;
    if (nextId !== sharedThreadId) {
      drafts.set(sharedThreadId || '', q.value);
      q.value = drafts.get(nextId || '') || '';
      q.style.height = 'auto';
      sharedThreadId = nextId;
      editor = null;
      if (nextId) setMode('ask');
      document.getElementById('topic-form').hidden = true;
    }
    const nextTopics = JSON.stringify((msg.threads || []).map(t => [t.id, t.title]));
    if (nextTopics !== topicSignature) {
      topicSignature = nextTopics;
      topicSelect.replaceChildren();
      const local = document.createElement('option');
      local.value = '';
      local.textContent = 'Local knowledge console';
      topicSelect.appendChild(local);
      for (const thread of msg.threads || []) {
        const option = document.createElement('option');
        option.value = thread.id;
        option.textContent = thread.title;
        topicSelect.appendChild(option);
      }
    }
    topicSelect.value = sharedThreadId || '';
    sharedBusy = !!msg.busy;
    sendBtn.disabled = !!sharedThreadId && sharedBusy;
    document.getElementById('topic-new').disabled = sharedBusy;
    topicStatus.textContent = msg.problem || (sharedThreadId ? 'Shared through Cinder · library Q&A via the Cinder provider' : 'Shared topics · choose a topic or create one');
    topicStatus.className = msg.problem ? 'error' : '';
    const signature = JSON.stringify([sharedThreadId, msg.turns]);
    if (signature !== sharedSignature) {
      sharedSignature = signature;
      turns = msg.turns || [];
      renderFeed();
    }
  } else if (msg.type === 'init') {
    mode = msg.mode || 'ask';
    scope = msg.scope || scope;
    turns = msg.turns || [];
    setMode(mode);
    renderScope();
    renderFeed();
  } else if (msg.type === 'turn') {
    if (sharedThreadId) return;
    const turn = msg.turn;
    const at = turns.findIndex((t) => t.id === turn.id);
    if (at >= 0) turns[at] = turn;
    else turns.push(turn);
    renderFeed();
  } else if (msg.type === 'announce') {
    announcement = { level: msg.level || 'info', message: msg.message || '' };
    renderAnnounce();
  } else if (msg.type === 'scope') {
    scope = msg.scope || scope;
    renderScope();
  } else if (msg.type === 'mode') {
    setMode(msg.mode);
  } else if (msg.type === 'editor') {
    renderEditor(msg.turnId, msg.cardType, msg.cardId, msg.draft, msg.projects || [], !!msg.proposal, msg.warning);
  } else if (msg.type === 'editorError') {
    showEditorError(msg.message);
  } else if (msg.type === 'saved') {
    editor = null;
    renderFeed();
  } else if (msg.type === 'created') {
    // The new note joined the turn as a card of its own: open it, so the
    // reader sees what was just written.
    editor = null;
    expandedCards.add(msg.turnId + '/' + msg.cardType + '/' + msg.cardId);
    renderFeed();
  } else if (msg.type === 'projectCandidates') {
    candidates = msg.candidates || [];
    renderScope();
    if (!popEl.hidden) renderPop();
  } else if (msg.type === 'aiSettings') {
    aiBase.value = msg.settings.baseUrl || '';
    aiModel.value = msg.settings.model || '';
    aiKey.value = '';
    aiKey.placeholder = msg.settings.hasKey
      ? 'a key is stored — leave empty to keep it'
      : 'no key stored yet';
  } else if (msg.type === 'aiModels') {
    const models = msg.models || [];
    aiPicker.innerHTML = '';
    if (models.length) {
      for (const m of models) {
        const opt = document.createElement('option');
        opt.value = m;
        opt.textContent = m;
        aiPicker.appendChild(opt);
      }
      aiPicker.hidden = false;
    } else {
      aiPicker.hidden = true;
    }
  } else if (msg.type === 'aiStatus') {
    aiStatus.textContent = msg.message || '';
    aiStatus.className = 'sheet-status ' + (msg.level || 'info');
  }
});

setMode('ask');
renderScope();
renderFeed();
// The host answers with the init message — a push before this script ran
// would be dropped, and the panel would come up empty.
vscode.postMessage({ type: 'ready' });
</script>
</body>
</html>`;
}
