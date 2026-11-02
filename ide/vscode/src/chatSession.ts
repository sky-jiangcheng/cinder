// The conversation model behind the IDE chat console (ADR-0036).
//
// The panel is a conversation: a question goes in, a turn comes out, and the
// turns stay in the feed. This file owns that model — the data, the tool
// calls and the scope. The webview owns the fold state (which turns are
// collapsed), so a collapsed turn survives the next turn arriving.
//
// The thin-client discipline is unchanged and load-bearing here: the plugin
// decides WHICH tool to call and WHAT to show, never how to retrieve or rank.
// Every answer comes from the shared Go service layer:
//
//   ask   -> repotory_ask          (answer + sources, ADR-0033)
//   notes -> repotory_notes_search (hits; NO project filter — see below)
//   pages -> repotory_wiki_search  (approved pages, ADR-0032)
//
// The project lock (ADR-0036 decision 2) is a DEFAULT, not a restriction:
//
//   ask   -> the project id goes to the tool: the server really filters.
//   pages -> the project id goes to the tool: the server really filters.
//   notes -> the tool has NO project parameter, so the lock is a PRESENTATION
//           filter over the returned hits. The cost is real (a project can
//           come back with fewer than N hits) and the panel says so instead
//           of pretending the count is everything.
import {
  excerpt,
  hitText,
  parseAskAnswer,
  parseOrganizeProposal,
  parseProjects,
  parseSearchHits,
  parseWikiPages,
  parseWikiRead,
  servesWebUi,
  type NoteHit,
  type OrganizeProposal,
  type WikiPageSummary,
} from "./headless.js";
import type {
  Detail,
  EditorDraft,
  ExtMessage,
  HitCard,
  Mode,
  ProjectOption,
  ScopeState,
  Turn,
  ViewMessage,
} from "./chatTypes.js";

/**
 * Everything the session needs from the outside world.
 *
 * A port, not a dependency on vscode: the session is testable without an
 * editor, and the MCP/HTTP split stays in one place (the extension host).
 */
export interface Transport {
  /** Call an MCP tool and return its text content. */
  callTool(name: string, args: Record<string, unknown>): Promise<string>;
  /** Call a headless-service RPC method (the bridge MCP does not carry). */
  rpc(method: string, args: unknown[]): Promise<unknown>;
  /** The headless service's base URL. */
  serverUrl(): string;
  /** The folder the session is standing in, if any. */
  workspacePath(): string | undefined;
  /** Open a URL outside the IDE. */
  openExternal(url: string): Promise<void>;
}

interface WorkspaceResolution {
  Project?: { id: number; name: string; root_path: string } | null;
  Repository?: { id: number; path: string } | null;
}

/** The prefix the ask tool uses when the configured AI endpoint failed. */
const ASK_ERROR_PREFIX = "error: ";

/** The prefix the ask tool uses when no AI endpoint is configured at all. */
const ASK_NO_ENDPOINT_PREFIX = "No AI endpoint is configured";

export class ChatSession {
  private turns: Turn[] = [];
  private counter = 0;
  private mode: Mode = "ask";
  private scope: ScopeState = { projectId: 0, projectName: "", repository: "", locked: false };

  /** Set by the extension: the scope changed (the status bar shows it). */
  onScopeChanged?: (scope: ScopeState) => void;

  constructor(
    private readonly transport: Transport,
    private readonly post: (message: ViewMessage) => void,
  ) {}

  /** The panel came on screen: hand it the state it needs to render. */
  init(): void {
    this.post({ type: "init", mode: this.mode, scope: this.scope, turns: this.turns });
    void this.refreshScope();
  }

  /** The current scope, for the status bar and the command palette. */
  currentScope(): ScopeState {
    return this.scope;
  }

  /** A one-line description of the scope ("Repotory · pay-common"). */
  scopeLabel(): string {
    if (!this.scope.projectId) return "";
    return this.scope.repository
      ? `${this.scope.projectName} · ${this.scope.repository}`
      : this.scope.projectName;
  }

  /**
   * Resolve the workspace into a project and lock the panel to it.
   *
   * The matching rules live server-side on purpose: the MCP tools resolve the
   * same path, and a TypeScript reimplementation would let the panel and an
   * agent's repotory_context disagree about which knowledge a session gets.
   *
   * Without the headless service there is no project id to lock to — the
   * panel then says so and searches across projects, rather than pretending
   * it is scoped.
   */
  async refreshScope(): Promise<void> {
    const dir = this.transport.workspacePath();
    if (!dir) {
      this.setScope({ projectId: 0, projectName: "", repository: "", locked: false });
      return;
    }
    try {
      const res = (await this.transport.rpc("ResolveWorkspacePath", [dir])) as WorkspaceResolution | null;
      if (res?.Project) {
        const repository = res.Repository?.path
          ? res.Repository.path.split(/[\\/]/).pop() ?? ""
          : "";
        this.setScope({
          projectId: Number(res.Project.id ?? 0),
          projectName: res.Project.name ?? "",
          repository,
          locked: true,
        });
        return;
      }
      this.setScope({ projectId: 0, projectName: "", repository: "", locked: false });
      this.announce(
        "warn",
        "This folder is not in the knowledge base yet — the panel searches across all projects.",
      );
    } catch {
      this.setScope({ projectId: 0, projectName: "", repository: "", locked: false });
      this.announce(
        "warn",
        `The local Repotory service is not reachable at ${this.transport.serverUrl()} — the project lock needs it, so the panel searches across all projects.`,
      );
    }
  }

  /** The messages the webview sends. */
  async handle(msg: ExtMessage): Promise<void> {
    switch (msg.type) {
      case "submit":
        await this.run(msg.query, msg.mode);
        break;
      case "mode":
        this.mode = msg.mode;
        break;
      case "scopeToggle":
        await this.toggleScope();
        break;
      case "expandCard":
        await this.expandCard(msg.turnId, msg.cardType, msg.cardId);
        break;
      case "open":
        await this.openHit(msg.card);
        break;
      case "edit":
        await this.openEditorForNote(msg.turnId, msg.cardType, msg.cardId);
        break;
      case "create":
        await this.openEditorForNew(msg.turnId, msg.cardType, msg.cardId);
        break;
      case "organize":
        await this.organizeNote(msg.turnId, msg.cardType, msg.cardId);
        break;
      case "save":
        await this.saveDraft(msg.turnId, msg.cardType, msg.cardId, msg.draft);
        break;
      case "cancelEdit":
        // The webview already restored the card on its own; nothing to undo.
        break;
      default:
        break;
    }
  }

  /**
   * Ask one question and append the turn to the feed.
   *
   * Public because the command palette asks the same way: a command appends a
   * turn to the conversation instead of replacing its result (ADR-0036).
   */
  async run(query: string, mode?: Mode): Promise<void> {
    const text = query.trim();
    if (!text) return;
    const turn: Turn = {
      id: `turn-${++this.counter}`,
      mode: mode ?? this.mode,
      query: text,
      state: "pending",
    };
    this.turns.push(turn);
    this.post({ type: "turn", turn });
    try {
      if (turn.mode === "ask") await this.runAsk(turn);
      else if (turn.mode === "notes") await this.runNotes(turn);
      else await this.runPages(turn);
    } catch (err) {
      turn.state = "error";
      turn.error = err instanceof Error ? err.message : String(err);
      this.post({ type: "turn", turn });
      this.announce("error", turn.error);
    }
  }

  // --------------------------------------------------------------- the turns

  /**
   * Ask mode: `repotory_ask` answers with a reply and a source list (ADR-0033).
   *
   * Two failure shapes have to be told apart. The tool reports a configured
   * endpoint that failed as a TEXT error, not as a protocol error — it comes
   * back as a normal result whose body starts with "error: ". Treating that as
   * an answer would put a failure in the feed in the shape of knowledge.
   */
  private async runAsk(turn: Turn): Promise<void> {
    const args: Record<string, unknown> = { query: turn.query };
    if (this.scope.locked && this.scope.projectId) args.project_id = this.scope.projectId;
    const text = await this.transport.callTool("repotory_ask", args);

    if (text.startsWith(ASK_ERROR_PREFIX)) {
      throw new Error(text.slice(ASK_ERROR_PREFIX.length).trim() || "The AI endpoint failed.");
    }
    if (text.startsWith(ASK_NO_ENDPOINT_PREFIX)) {
      this.announce(
        "warn",
        "No AI endpoint is configured — Repotory answered with keyword matches, not an answer.",
      );
    }

    const parsed = parseAskAnswer(text);
    turn.state = "done";
    // A source list that does not parse is shown VERBATIM: the raw text still
    // carries the sources, and a half-parsed list would hide which ones.
    // The order matters: the verbatim fallback is decided FIRST, and the
    // empty-answer note only applies to a reply that really came back empty.
    turn.answer = parsed.parsed ? parsed.answer : parsed.raw;
    turn.verbatim = !parsed.parsed;
    turn.sources = parsed.parsed ? parsed.sources : [];
    if (parsed.parsed && !parsed.answer.trim()) {
      // An empty answer is a fact, not a blank panel: say so rather than
      // showing nothing under the question.
      turn.answer = "The service returned an empty answer.";
    }
    this.post({ type: "turn", turn });
  }

  /**
   * Notes mode: `repotory_notes_search`.
   *
   * The tool has NO project parameter — `path` only re-ranks. So the project
   * lock is applied HERE, over the returned hits. The panel says how many of
   * them belong to the locked project, because "3 hits" would otherwise read
   * as "3 hits in this project" and be wrong.
   */
  private async runNotes(turn: Turn): Promise<void> {
    const args: Record<string, unknown> = { query: turn.query };
    const dir = this.transport.workspacePath();
    if (dir) args.path = dir;
    const text = await this.transport.callTool("repotory_notes_search", args);
    const cards = parseSearchHits(text).map(toCard);
    if (this.scope.locked && this.scope.projectId) {
      const kept = cards.filter((card) => card.project_id === this.scope.projectId);
      turn.cards = kept;
      if (kept.length !== cards.length) {
        turn.scopeNote =
          `Locked to ${this.scope.projectName}: ${kept.length} of ${cards.length} matches shown. ` +
          `The notes tool has no project filter, so the panel filtered the list.`;
      }
    } else {
      turn.cards = cards;
    }
    turn.state = "done";
    this.post({ type: "turn", turn });
  }

  /**
   * Pages mode: `repotory_wiki_search`.
   *
   * Here the project scope IS a server-side filter: a project scope means
   * "that project's pages PLUS the global ones" (ADR-0031), which is why the
   * scope note mentions the global pages.
   */
  private async runPages(turn: Turn): Promise<void> {
    const args: Record<string, unknown> = { query: turn.query };
    const locked = this.scope.locked && this.scope.projectId > 0;
    if (locked) args.project_id = this.scope.projectId;
    const text = await this.transport.callTool("repotory_wiki_search", args);
    turn.cards = parseWikiPages(text).map(toWikiCard);
    if (locked) {
      turn.scopeNote = `Scoped to ${this.scope.projectName} — that project's pages and the global ones.`;
    }
    turn.state = "done";
    this.post({ type: "turn", turn });
  }

  // ------------------------------------------------------------- the scope

  private setScope(scope: ScopeState): void {
    this.scope = scope;
    this.post({ type: "scope", scope });
    this.onScopeChanged?.(scope);
  }

  /**
   * Unlock (or re-lock) the project.
   *
   * The lock is a default, not a restriction: opening the chip puts the panel
   * back to cross-project, and the chip says which of the two it is.
   */
  private async toggleScope(): Promise<void> {
    if (!this.scope.projectId) {
      this.announce(
        "warn",
        "This workspace does not resolve to a Repotory project yet, so there is nothing to lock to.",
      );
      return;
    }
    const locked = !this.scope.locked;
    this.setScope({ ...this.scope, locked });
    this.announce(
      "info",
      locked
        ? `Locked to ${this.scope.projectName}.`
        : `Searching across all projects — ${this.scope.projectName} is not filtered in.`,
    );
  }

  // ------------------------------------------------------------- the cards

  /**
   * One card of a turn, by type AND id.
   *
   * Notes and todos are separate id sequences (ADR-0032), so one turn can
   * hold a note 12 and a todo 12 at the same time — looking a card up by id
   * alone would open the wrong one.
   */
  private findCard(turn: Turn | undefined, cardType: string, cardId: number): HitCard | undefined {
    return (turn?.cards ?? []).find((c) => c.type === cardType && c.id === cardId);
  }

  /** Read the full body of a card, in place, inside its turn. */
  private async expandCard(turnId: string, cardType: string, cardId: number): Promise<void> {
    const turn = this.turns.find((t) => t.id === turnId);
    if (!turn) return;
    const card = this.findCard(turn, cardType, cardId);
    if (!card || card.detail) return;
    try {
      card.detail = await this.readDetail(card);
    } catch (err) {
      // The card must not sit on "Loading…" forever: it carries the failure
      // instead, so the reader sees WHY the body is missing.
      const message = err instanceof Error ? err.message : String(err);
      card.detail = {
        type: card.type,
        id: card.id,
        project_id: card.project_id,
        title: card.title,
        body: message,
        source: "error",
      };
      this.announce("error", message);
    }
    this.post({ type: "turn", turn });
  }

  /**
   * The full body of one hit.
   *
   * Notes have a row to read (repotory_notes_read → the whole note); todos
   * do not — the matched excerpt is the most the search contract returns, so
   * the card says "excerpt" rather than pretending it is the full item. An
   * approved knowledge page is read with the page tool (ADR-0032): the two id
   * spaces are separate, so the wrong tool would answer about the wrong thing.
   */
  private async readDetail(card: HitCard): Promise<Detail> {
    if (card.type === "wiki") {
      try {
        const page = parseWikiRead(await this.transport.callTool("repotory_wiki_read", { id: card.id }));
        if (page) {
          return {
            type: "wiki",
            id: page.id,
            project_id: page.project_id,
            title: page.title,
            body: page.content,
            source: "wiki",
            updated_at: page.updated_at,
            pageKind: page.kind,
            slug: page.slug,
          };
        }
      } catch {
        /* fall through to the excerpt below */
      }
      return {
        type: "wiki",
        id: card.id,
        project_id: card.project_id,
        title: card.title,
        body: card.snippet,
        source: "snippet",
        pageKind: card.pageKind,
        slug: card.slug,
      };
    }
    if (card.type === "todo") {
      // A todo has NO row to read: the matched excerpt is all the search
      // contract returns. Asking for it by id would answer about the NOTE
      // with that id — notes and todos are separate id sequences (ADR-0032),
      // so the ids can collide and the reader would get the wrong item.
      return {
        type: "todo",
        id: card.id,
        project_id: card.project_id,
        title: card.title,
        body: card.snippet,
        source: "snippet",
      };
    }
    try {
      const detail = parseNoteRead(await this.transport.callTool("repotory_notes_read", { id: card.id }));
      if (detail) return detail;
    } catch {
      /* fall through to the snippet below */
    }
    return {
      type: card.type,
      id: card.id,
      project_id: card.project_id,
      title: card.title,
      body: card.snippet,
      source: "snippet",
    };
  }

  /**
   * Deep-link a hit into the Repotory web UI.
   *
   * Deep links are best-effort (ADR-0035 decision 6): a headless server
   * started without `--web-dist` answers the API but 404s every other path, so
   * opening the URL would drop the user on a dead page with no explanation at
   * all. The probe asks the server whether it serves the UI first — the same
   * question, the same way, the JetBrains plugin asks.
   */
  private async openHit(card: HitCard | undefined): Promise<void> {
    if (!card) return;
    const route =
      card.type === "wiki"
        ? `/wiki/${card.id}`
        : card.type === "todo"
          ? `/project/${card.project_id}?tab=todos`
          : `/project/${card.project_id}?noteId=${card.id}`;
    const base = this.transport.serverUrl().replace(/\/+$/, "");
    if (!(await servesWebUi(base))) {
      this.announce(
        "warn",
        "The local service serves the API only (no --web-dist), so that link would open a 404. A local service with the web UI is needed.",
      );
      return;
    }
    await this.transport.openExternal(`${base}${route}`);
  }

  // ------------------------------------------------------- note maintenance

  /** The project list for the editor's project dropdown. */
  private async fetchProjectOptions(): Promise<ProjectOption[]> {
    try {
      return parseProjects(await this.transport.callTool("repotory_projects_list", {}));
    } catch {
      // A missing dropdown degrades to the read-only project chip; the rest of
      // the editor works without it.
      return [];
    }
  }

  /** Open the editor on an existing note (its stored fields). */
  private async openEditorForNote(turnId: string, cardType: string, cardId: number): Promise<void> {
    try {
      const detail = parseNoteRead(await this.transport.callTool("repotory_notes_read", { id: cardId }));
      if (!detail) {
        this.announce("error", `Note ${cardId} could not be read — nothing to edit.`);
        return;
      }
      const projects = await this.fetchProjectOptions();
      this.post({
        type: "editor",
        turnId,
        cardType,
        cardId,
        draft: {
          id: detail.id,
          project_id: detail.project_id,
          title: detail.title,
          tags: detail.tags ?? "",
          kind: detail.kind ?? "knowledge",
          content: detail.body,
        },
        projects,
        proposal: false,
      });
    } catch (err) {
      this.announce("error", err instanceof Error ? err.message : String(err));
    }
  }

  /** Open the editor on a new note under the card's project. */
  private async openEditorForNew(turnId: string, cardType: string, cardId: number): Promise<void> {
    const turn = this.turns.find((t) => t.id === turnId);
    const card = this.findCard(turn, cardType, cardId);
    const projectId = card?.project_id ?? this.scope.projectId;
    if (!projectId) {
      this.announce("error", "There is no project to put the new note in.");
      return;
    }
    const projects = await this.fetchProjectOptions();
    this.post({
      type: "editor",
      turnId,
      cardType,
      cardId,
      draft: { id: 0, project_id: projectId, title: "", tags: "", kind: "knowledge", content: "" },
      projects,
      proposal: false,
    });
  }

  /**
   * Run the AI tidy and open its PROPOSAL in the editor.
   *
   * The service writes nothing: the proposal lands in the same editor a manual
   * edit uses, so reviewing machine-generated wording IS the edit flow, and
   * only the user's save persists it (ADR-0015: the human gate is the save).
   */
  private async organizeNote(turnId: string, cardType: string, cardId: number): Promise<void> {
    try {
      const text = await this.transport.callTool("repotory_notes_organize", { id: cardId });
      const proposal: OrganizeProposal | undefined = parseOrganizeProposal(text);
      if (!proposal) {
        this.announce("error", "AI tidy failed: the service returned no usable proposal.");
        return;
      }
      const stored = parseNoteRead(await this.transport.callTool("repotory_notes_read", { id: cardId }));
      const projects = await this.fetchProjectOptions();
      this.post({
        type: "editor",
        turnId,
        cardType,
        cardId,
        draft: {
          id: proposal.note_id || cardId,
          project_id: stored?.project_id ?? 0,
          title: proposal.title,
          tags: proposal.tags,
          kind: stored?.kind ?? "knowledge",
          content: proposal.content,
        },
        projects,
        proposal: true,
        warning: proposal.warning,
      });
    } catch (err) {
      this.announce("error", err instanceof Error ? err.message : String(err));
    }
  }

  /**
   * Persist an editor draft: update in place, or create under the project.
   *
   * Fields are written first, the project move second: a failed move (the HTTP
   * service is down — MCP works without it) must not lose the edit the user
   * just made. After a successful save the freshly written note replaces the
   * editor, so what is on screen is what is in the store.
   */
  private async saveDraft(
    turnId: string,
    cardType: string,
    cardId: number,
    draft: EditorDraft,
  ): Promise<void> {
    const turn = this.turns.find((t) => t.id === turnId);
    try {
      if (draft.id) {
        const updatedText = await this.transport.callTool("repotory_notes_update", {
          id: draft.id,
          content: draft.content,
          title: draft.title,
          tags: draft.tags,
          category: draft.kind,
        });
        let detail = parseNoteRead(updatedText);
        if (!detail) {
          // The update succeeded (no throw) but the payload is unusable: one
          // more read gets the truth rather than trusting a guess.
          detail = parseNoteRead(await this.transport.callTool("repotory_notes_read", { id: draft.id }));
        }
        if (!detail) {
          this.post({
            type: "editorError",
            turnId,
            cardType,
            cardId,
            message: "Saved, but reading the note back failed — reopen it to see the result.",
          });
          return;
        }
        if (draft.project_id && detail.project_id && draft.project_id !== detail.project_id) {
          try {
            await this.moveNoteViaRpc(draft.id, draft.project_id);
            detail = { ...detail, project_id: draft.project_id };
          } catch (err) {
            this.post({
              type: "editorError",
              turnId,
              cardType,
              cardId,
              message: `Fields saved, but moving the note failed: ${
                err instanceof Error ? err.message : String(err)
              }`,
            });
            return;
          }
        }
        if (turn) {
          const card = this.findCard(turn, cardType, cardId);
          if (card) {
            card.title = detail.title;
            card.detail = detail;
          }
          this.post({ type: "turn", turn });
        }
        this.post({ type: "saved", turnId, cardType, cardId, detail });
        return;
      }

      const createdText = await this.transport.callTool("repotory_notes_create", {
        project_id: draft.project_id,
        title: draft.title,
        content: draft.content,
        tags: draft.tags,
        category: draft.kind,
      });
      const created = parseNoteRead(createdText);
      if (!created) {
        this.post({
          type: "editorError",
          turnId,
          cardType,
          cardId,
          message: "Created, but reading the note back failed — search for it to see the result.",
        });
        return;
      }
      if (turn) {
        turn.cards = [
          ...(turn.cards ?? []),
          { type: "note", id: created.id, project_id: created.project_id, title: created.title, snippet: "" },
        ];
        this.post({ type: "turn", turn });
      }
      this.post({ type: "created", turnId, cardType: "note", cardId: created.id, detail: created });
    } catch (err) {
      this.post({
        type: "editorError",
        turnId,
        cardType,
        cardId,
        message: `Save failed: ${err instanceof Error ? err.message : String(err)}`,
      });
    }
  }

  /**
   * Move a note to another project over the HTTP RPC bridge.
   *
   * The MCP surface has no move tool on purpose (MoveNote is registered
   * desktop-only: re-filing knowledge is a human decision an agent must not
   * make), and the IDE panel is a human-driven client.
   */
  private async moveNoteViaRpc(noteId: number, projectId: number): Promise<void> {
    const response = (await this.transport.rpc("MoveNote", [noteId, projectId])) as {
      error?: string;
    } | null;
    if (response?.error) throw new Error(response.error);
  }

  private announce(level: "info" | "warn" | "error", message: string): void {
    this.post({ type: "announce", level, message });
  }
}

// ------------------------------------------------------------------ helpers

function toCard(hit: NoteHit): HitCard {
  return {
    type: hit.type ?? "note",
    id: Number(hit.id ?? 0),
    project_id: Number(hit.project_id ?? 0),
    title:
      typeof hit.title === "string" && hit.title
        ? hit.title
        : excerpt(hitText(hit), 40) || `note ${String(hit.id ?? "?")}`,
    snippet: excerpt(hitText(hit), 140),
  };
}

function toWikiCard(page: WikiPageSummary): HitCard {
  return {
    type: "wiki",
    id: Number(page.id ?? 0),
    project_id: Number(page.project_id ?? 0),
    title:
      typeof page.title === "string" && page.title ? page.title : `page ${String(page.id ?? "?")}`,
    snippet: excerpt(typeof page.snippet === "string" ? page.snippet : "", 140),
    pageKind: typeof page.kind === "string" ? page.kind : "",
    slug: typeof page.slug === "string" ? page.slug : "",
  };
}

/**
 * Parse the `repotory_notes_read` tool result into the fields a card shows.
 */
export function parseNoteRead(text: string): Detail | undefined {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return undefined;
  }
  const obj = parsed as Record<string, unknown> | null;
  if (!obj || typeof obj !== "object") return undefined;
  const note = (obj.note ?? obj) as Record<string, unknown>;
  const id = Number(note.id ?? 0);
  if (!id) return undefined;
  return {
    type: "note",
    id,
    project_id: Number(note.project_id ?? 0),
    title: typeof note.title === "string" && note.title ? note.title : `note ${id}`,
    body: typeof note.content === "string" ? note.content : "",
    source: "note",
    updated_at: typeof note.updated_at === "string" ? note.updated_at : undefined,
    tags: typeof note.tags === "string" && note.tags ? note.tags : undefined,
    kind: typeof note.kind === "string" && note.kind ? note.kind : undefined,
  };
}
