// Cinder chat console — the IDE panel, rewritten as a CONVERSATION.
//
// ADR-0036 is the design source. The panel used to be a searcher: a box, a
// list of thumbnails, a detail pane. It is now a conversation feed:
//
//   ┌───────────────────────────────────────────────┐
//   │ brand header                                  │  ← identity
//   ├───────────────────────────────────────────────┤
//   │ announcement bar (status, errors, dismissible)│  ← ADR-0036 decision 5
//   ├───────────────────────────────────────────────┤
//   │                                               │
//   │   the feed: one turn per question             │  ← the only scroll area
//   │   ▸ N earlier turns ▾                         │  ← decision 3
//   │                                               │
//   ├───────────────────────────────────────────────┤
//   │ [Ask][Notes][Pages]        [🔒 Foo]   [Send]  │  ← decision 6
//   │ ┌───────────────────────────────────────────┐ │
//   │ │ ask about this project…                   │ │
//   │ └───────────────────────────────────────────┘ │
//   └───────────────────────────────────────────────┘
//
// The detail of an item opens IN PLACE, inside the turn that produced it —
// there is no second pane (decision 7), because a narrow sidebar split in two
// squeezes the detail into a few unreadable columns.
//
// The DATA lives in the extension host; the FOLD STATE lives here. A collapsed
// turn must stay collapsed while other turns arrive, which is exactly the
// Cline panel's known pain point (its collapse switches reset every time the
// panel is reopened).
//
// The thin-client discipline is unchanged: this file renders, it never
// searches. Queries go to the host (repotory-mcp over stdio, the same path
// the commands use) and answers come back as messages.
import * as vscode from "vscode";
import { consoleHtml } from "./chatConsoleHtml.js";
import type { ExtMessage, ViewMessage } from "./chatTypes.js";

/**
 * The webview side of the chat console.
 *
 * It owns the HTML, the messages and the fold state. It owns no knowledge:
 * every answer comes from the host, which talks to repotory-mcp.
 */
export class RepotoryChatConsoleProvider implements vscode.WebviewViewProvider {
  private view: vscode.WebviewView | undefined;

  /** Set by the extension: the panel's message handler. */
  onMessage?: (msg: ExtMessage) => void | Promise<void>;
  /** Set by the extension: the panel is on screen and wants its state. */
  onReady?: () => void;

  resolveWebviewView(
    webviewView: vscode.WebviewView,
    _context: vscode.WebviewViewResolveContext,
    _token: vscode.CancellationToken,
  ): void {
    this.view = webviewView;
    webviewView.webview.options = { enableScripts: true };
    webviewView.webview.html = consoleHtml(nonce());

    webviewView.webview.onDidReceiveMessage((msg: ExtMessage) => {
      if (msg.type === "copy") {
        void vscode.env.clipboard.writeText(msg.text);
        return;
      }
      if (msg.type === "ready") {
        // The webview's script is up: now it can receive the state.
        this.onReady?.();
        return;
      }
      void this.onMessage?.(msg);
    });
  }

  post(message: ViewMessage): void {
    void this.view?.webview.postMessage(message);
  }
}

function nonce(): string {
  const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  let out = "";
  for (let i = 0; i < 32; i++) out += chars.charAt(Math.floor(Math.random() * chars.length));
  return out;
}
