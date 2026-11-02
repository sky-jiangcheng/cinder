// Render the chat console outside VS Code, so the webview can be looked at.
//
// The panel is a few hundred lines of script inside a webview: no unit test
// can reach it, and "it compiles" is not "it renders". This harness writes a
// standalone HTML file with the SAME markup and script the extension ships,
// plus a stub `acquireVsCodeApi` that feeds it a plausible conversation.
//
//   npm run compile && node scripts/preview-console.mjs [out.html]
//
// The stub is deliberately dumb: it answers `init` with a few turns and
// records every message the panel sends, so the wiring can be read off the
// page (the recorded messages land in the browser console).
import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { consoleHtml } from "../out/chatConsoleHtml.js";

const nonce = "previewpreviewpreviewpreview";
const out = resolve(process.argv[2] ?? "/tmp/repotory-console-preview.html");

const turns = [
  {
    id: "turn-1",
    mode: "ask",
    query: "why do we retry on 503?",
    state: "done",
    answer:
      "The gateway retries a 503 because the upstream pod may still be rolling out. " +
      "The retry lives in `pay-gateway`, not in the client: the client only sees the 503.",
    sources: [
      { ref: "P1", type: "page", title: "Retry policy", id: 42, slug: "retry-policy", project: "CBiPay" },
      { ref: "N3", type: "note", title: "503 notes", id: 7, project: "CBiPay" },
    ],
  },
  {
    id: "turn-2",
    mode: "notes",
    query: "deploy hook",
    state: "done",
    cards: [
      {
        type: "note",
        id: 12,
        project_id: 4,
        title: "Deploy hook runs on the merge queue",
        snippet: "The hook is wired to the merge queue, not to the branch build…",
      },
      { type: "todo", id: 19, project_id: 4, title: "Move the deploy hook to CI", snippet: "…" },
    ],
    scopeNote:
      "Locked to CBiPay: 2 of 5 matches shown. The notes tool has no project filter, so the panel filtered the list.",
  },
  {
    id: "turn-3",
    mode: "pages",
    query: "ADR-0021",
    state: "done",
    cards: [
      {
        type: "wiki",
        id: 45,
        project_id: 0,
        title: "ADR-0021: Note editing is approval-gated",
        snippet: "Approved 2026-10-02 · concept",
        pageKind: "concept",
        slug: "adr-0021",
      },
    ],
    scopeNote: "Scoped to CBiPay — that project's pages and the global ones.",
  },
];

const stub = `
window.__posted = [];
window.acquireVsCodeApi = function () {
  return {
    postMessage: function (msg) {
      window.__posted.push(msg);
      console.log('panel ->', JSON.stringify(msg));
    },
    getState: function () { return undefined; },
    setState: function () {},
  };
};
`;

const html = consoleHtml(nonce)
  // The stub has to exist BEFORE the panel's script runs, and the CSP only
  // allows the nonce — so it carries the same one.
  .replace(
    `<script nonce="${nonce}">`,
    `<script nonce="${nonce}">${stub}</script>\n<script nonce="${nonce}">`,
  )
  .replace(
    "</body>",
    `<script nonce="${nonce}">
  window.addEventListener('load', function () {
    var api = { type: 'init', mode: 'ask', scope: { projectId: 4, projectName: 'CBiPay', repository: 'pay-common', locked: true }, turns: ${JSON.stringify(turns)} };
    window.postMessage(api, '*');
    setTimeout(function () {
      window.postMessage({ type: 'announce', level: 'warn', message: 'The local Repotory service is not reachable at http://127.0.0.1:18765 — the project lock needs it, so the panel searches across all projects.' }, '*');
    }, 50);
  });
</script>
</body>`,
  );

writeFileSync(out, html, "utf8");
console.log(`wrote ${out}`);
