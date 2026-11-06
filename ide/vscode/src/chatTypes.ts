// The contract between the extension host and the chat-console webview.
//
// One file for the whole shape, so a renamed field cannot drift into a
// silently ignored message: the webview renders these, the host fills them,
// and both sides import the same types.
//
// ADR-0036 is the design source: the panel is a CONVERSATION, not a result
// list. A turn is one question and its answer; the turns stay in the feed, the
// old ones fold away, and the detail of an item opens IN PLACE inside the turn
// that produced it — there is no second pane.
//
// The data lives in the host, the FOLD STATE lives in the webview: a collapsed
// turn must stay collapsed while other turns arrive, which is exactly the
// Cline panel's known pain point (its collapse switches reset every time the
// panel is reopened).

/** Which knowledge face a turn talks to. Ask is the default (ADR-0036 decision 4). */
export type Mode = "ask" | "notes" | "pages";

/**
 * The panel's scope: which project it is locked to.
 *
 * `locked` is a DEFAULT, not a restriction — the chip opens and the panel goes
 * back to cross-project. `projectId` is 0 when the workspace resolves to no
 * project, and then there is nothing to lock to.
 */
export interface ScopeState {
  /** The project the workspace resolves to; 0 when it resolves to none. */
  projectId: number;
  /** The project's display name ("" when there is none). */
  projectName: string;
  /** The repository folder inside the project, when the workspace is in one. */
  repository: string;
  /** True when the panel filters to that project. */
  locked: boolean;
}

/**
 * One cited source of an `repotory_ask` answer.
 *
 * The MCP tool answers in TEXT (`[P1] page "Title" (id 42, project Foo)`), so
 * this is a parsed view of a text line, not a service type. It exists so the
 * reader can see what P1 is — a ref that means nothing is worse than no answer.
 */
export interface EvidenceSource {
  /** The bracketed ref the answer cites: "P1" (a page) or "N3" (a note). */
  ref: string;
  /** The id space the source lives in: "page" or "note". */
  type: string;
  /** The source's title. */
  title: string;
  /** The stable id within its own type. */
  id: number;
  /** The page slug (pages only). */
  slug?: string;
  /** The owning project's name. */
  project?: string;
}

/** One project as the editor's project dropdown needs it. */
export interface ProjectOption {
  id: number;
  name: string;
}


/** A fully-read item: the body of a note or of an approved knowledge page. */
export interface Detail {
  type: string;
  id: number;
  project_id: number;
  title: string;
  /** The full body for notes; the matched excerpt for todos (no row to read). */
  body: string;
  source: "note" | "wiki" | "snippet" | "error";
  updated_at?: string;
  tags?: string;
  /** The note's kind, so the editor can round-trip it (notes only). */
  kind?: string;
  /** The page kind of an approved knowledge page (concept / synthesis / entity). */
  pageKind?: string;
  /** The page slug, for an approved knowledge page. */
  slug?: string;
}

/** One search hit as a card in the feed. */
export interface HitCard {
  id: number;
  /** Which id space this hit belongs to — "note", "todo" or "wiki". */
  type: string;
  project_id: number;
  title: string;
  /** The matched excerpt. */
  snippet: string;
  /** The page kind of a knowledge page (concept / synthesis / entity). */
  pageKind?: string;
  /** The page's slug, shown next to its title. */
  slug?: string;
  /** The full body, once the card has been opened in place. */
  detail?: Detail;
}

/**
 * The parsed answer of `repotory_ask`.
 *
 * The tool answers in TEXT, so the source list has to be read back out of the
 * reply. `parsed` is the honest flag: when the source lines do not match the
 * contract, the whole text is shown VERBATIM instead of a half-list — a
 * swallowed source is worse than an unparsed one (ADR-0036).
 */
export interface AskAnswer {
  /** The reply without the source list; empty when the parse failed. */
  answer: string;
  /** The tool's raw text, always. */
  raw: string;
  /** The parsed sources; empty when the parse failed. */
  sources: EvidenceSource[];
  /** False when the source list could not be parsed — render `raw` verbatim. */
  parsed: boolean;
}

/** One question and everything it produced. */
export interface Turn {
  /** Host-assigned id; the webview uses it to address this turn. */
  id: string;
  mode: Mode;
  query: string;
  state: "pending" | "done" | "error";
  /** The answer (ask mode). */
  answer?: string;
  /**
   * True when the answer must be shown VERBATIM: the source list could not be
   * parsed, and showing the raw text is the honest fallback (ADR-0036: a
   * broken parse must never swallow the sources).
   */
  verbatim?: boolean;
  /** The parsed sources (ask mode). */
  sources?: EvidenceSource[];
  /** The hit cards (notes / pages mode). */
  cards?: HitCard[];
  /**
   * One line about what the project lock filtered away — the honest cost of a
   * presentation-layer filter (ADR-0036 decision 2).
   */
  scopeNote?: string;
  /** The failure text, when the turn failed. */
  error?: string;
}

/** The editable fields of a note. */
export interface EditorDraft {
  /** 0 = a new note under `project_id`. */
  id: number;
  project_id: number;
  title: string;
  tags: string;
  kind: string;
  content: string;
}

/**
 * webview → host.
 *
 * A card is addressed by its TYPE and its id, never by the id alone: notes
 * and todos are separate id sequences (ADR-0032), so one turn can hold a
 * note 12 and a todo 12 at the same time, and looking a card up by id alone
 * would open the wrong one.
 */
export type ExtMessage =
  | { type: "sharedSelect"; threadId: string | null }
  | { type: "sharedCreate"; title: string }
  | { type: "sharedRefresh" }
  /**
   * The webview's script is up.
   *
   * The host must not push `init` on its own: `postMessage` to a webview
   * whose script has not run yet is silently dropped, so the panel would
   * come up empty. The webview says hello, the host answers with the state.
   */
  | { type: "ready" }
  | { type: "submit"; query: string; mode: Mode }
  | { type: "mode"; mode: Mode }
  | { type: "scopeToggle" }
  | { type: "toggleTurn"; turnId: string }
  | { type: "toggleEarlier" }
  | { type: "toggleSources"; turnId: string }
  | { type: "expandCard"; turnId: string; cardType: string; cardId: number }
  | { type: "open"; turnId: string; card: HitCard }
  | { type: "edit"; turnId: string; cardType: string; cardId: number }
  | { type: "create"; turnId: string; cardType: string; cardId: number }
  | { type: "organize"; turnId: string; cardType: string; cardId: number }
  | { type: "save"; turnId: string; cardType: string; cardId: number; draft: EditorDraft }
  | { type: "cancelEdit"; turnId: string; cardType: string; cardId: number }
  // ---- the panel's own action strip (ADR-0044 decision 1) --------------------
  // These four used to live in VS Code's `view/title`, where the host drew
  // them at the far right of the sidebar title bar and cropped the third one
  // off the panel's edge. The webview cannot draw that strip, so the actions
  // moved into the header the panel draws itself.
  | { type: "openDiagnostics" }
  | { type: "openDesktop" }
  // ---- project recognition (ADR-0044 decision 6) ----------------------------
  | { type: "pickProject"; projectId: number };
/** host → webview. */
export type ViewMessage =
  | { type: "sharedSent"; threadId: string; query: string }
  | ({ type: "sharedState" } & import("./sharedChat.js").SharedChatState)
  | { type: "init"; mode: Mode; scope: ScopeState; turns: Turn[] }
  | { type: "turn"; turn: Turn }
  | { type: "announce"; level: "info" | "warn" | "error"; message: string }
  | { type: "scope"; scope: ScopeState }
  | { type: "mode"; mode: Mode }
  | {
      type: "editor";
      turnId: string;
      cardType: string;
      cardId: number;
      draft: EditorDraft;
      projects: ProjectOption[];
      proposal: boolean;
      warning?: string;
    }
  | { type: "editorError"; turnId: string; cardType: string; cardId: number; message: string }
  /** The editor closed with a saved note: the card shows the new body. */
  | { type: "saved"; turnId: string; cardType: string; cardId: number; detail: Detail }
  /** The editor closed with a created note: a new card joined the turn. */
  | { type: "created"; turnId: string; cardType: string; cardId: number; detail: Detail }
  /**
   * The projects the workspace could belong to.
   *
   * Sent when the workspace resolves to no project: the chip used to be
   * disabled there, which left the user with nothing to do (ADR-0044
   * decision 6).
   */
  | { type: "projectCandidates"; candidates: ProjectOption[] };
