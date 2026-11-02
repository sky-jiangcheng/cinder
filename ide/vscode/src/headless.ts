// Thin HTTP helper for the Repotory headless service (repotory server).
// The headless API is loopback-only by design (see internal/httpapi/server.go).
import * as http from "node:http";
import type { AskAnswer, EvidenceSource, ProjectOption } from "./chatTypes.js";

export function getJson(base: string, path: string): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const req = http.request(base + path, { method: "GET" }, (res) => {
      let data = "";
      res.setEncoding("utf8");
      res.on("data", (chunk: string) => {
        data += chunk;
      });
      res.on("end", () => {
        try {
          resolve(JSON.parse(data));
        } catch {
          reject(new Error(`invalid JSON from ${path}`));
        }
      });
    });
    req.on("error", reject);
    req.setTimeout(4000, () => {
      req.destroy(new Error(`timeout: ${path}`));
    });
    req.end();
  });
}

export function postJson(base: string, path: string, body: unknown): Promise<unknown> {
  const payload = JSON.stringify(body);
  return new Promise((resolve, reject) => {
    const req = http.request(
      base + path,
      {
        method: "POST",
        headers: { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(payload) },
      },
      (res) => {
        let data = "";
        res.setEncoding("utf8");
        res.on("data", (chunk: string) => {
          data += chunk;
        });
        res.on("end", () => {
          try {
            resolve(JSON.parse(data));
          } catch {
            reject(new Error(`invalid JSON from ${path}`));
          }
        });
      },
    );
    req.on("error", reject);
    req.setTimeout(4000, () => {
      req.destroy(new Error(`timeout: ${path}`));
    });
    req.end(payload);
  });
}

// NoteHit mirrors domain.SearchHit as it crosses the wire (internal/domain/
// types.go): type/id/project_id/title/snippet. There is no "content" field — the
// server sends only the matched excerpt (snippet). `content` is kept as a
// tolerated alias for older/alternative payloads, nothing more.
export interface NoteHit {
  type?: string;
  id?: number;
  project_id?: number;
  title?: string;
  snippet?: string;
  content?: string;
  [key: string]: unknown;
}

/**
 * Parse the text returned by the `repotory_notes_search` MCP tool into hits.
 *
 * The tool result is a *JSON array*, not an object: cmd/mcp/results.go's
 * makeJSONResult ignores its name argument and marshals []domain.SearchHit
 * directly, so the body starts with "[" — never "{". The sidebar used to slice
 * the document from its first "{" before parsing, which dropped the leading "["
 * of every real payload and made the whole list render as "No results".
 *
 * Whole-document parse first (the array, and the legacy {"hits":[…]} envelope,
 * both succeed here); only if that fails do we tolerate a preamble by retrying
 * from each candidate "[" / "{" boundary.
 */
export function parseSearchHits(text: string): NoteHit[] {
  const parsed = parseJsonLenient(text);
  if (parsed === undefined) return [];
  return toHits(parsed);
}

/**
 * Parse a tool result as JSON, tolerating a preamble.
 *
 * The tool results are bare JSON documents; the tolerant retry only runs when
 * the whole-document parse fails, so a well-formed payload is never re-cut.
 */
function parseJsonLenient(text: string): unknown {
  const trimmed = text.trim();
  if (!trimmed) return undefined;
  try {
    return JSON.parse(trimmed);
  } catch {
    /* fall through to the tolerant path */
  }
  const starts = [trimmed.indexOf("["), trimmed.indexOf("{")].filter((i) => i >= 0).sort((a, b) => a - b);
  for (const start of starts) {
    try {
      return JSON.parse(trimmed.slice(start));
    } catch {
      /* try the next boundary */
    }
  }
  return undefined;
}

function toHits(parsed: unknown): NoteHit[] {
  if (Array.isArray(parsed)) return parsed as NoteHit[];
  const hits = (parsed as { hits?: unknown } | null)?.hits;
  if (Array.isArray(hits)) return hits as NoteHit[];
  const notes = (hits as { notes?: unknown } | null)?.notes;
  if (Array.isArray(notes)) return notes as NoteHit[];
  return [];
}

/** The display text of a hit: the matched snippet, with `content` as a fallback. */
export function hitText(hit: NoteHit): string {
  if (typeof hit.snippet === "string" && hit.snippet) return hit.snippet;
  if (typeof hit.content === "string" && hit.content) return hit.content;
  return "";
}

/**
 * One approved knowledge page as it crosses the wire
 * (service.WikiPageSummary): identity, kind, scope and the matched excerpt.
 *
 * `project_id` is 0 for a global page (ADR-0031): it speaks for no single
 * project, which the reader must be able to SEE rather than infer.
 */
export interface WikiPageSummary {
  id?: number;
  slug?: string;
  title?: string;
  kind?: string;
  project_id?: number;
  updated_at?: string;
  snippet?: string;
  [key: string]: unknown;
}

/** One approved page in full (service.WikiPageDetail). */
export interface WikiPageDetail {
  id: number;
  slug: string;
  title: string;
  kind: string;
  project_id: number;
  updated_at: string;
  content: string;
  source: string;
}

/**
 * True when the running server also serves the Repotory web UI.
 *
 * A headless server started without `--web-dist` answers the API but 404s
 * every other path, so a deep link opened against it lands the user on a dead
 * page with no explanation. The probe asks for the SPA root and requires an
 * HTML answer: a 404 from the API mux, or a JSON body, means there is no web
 * UI here.
 *
 * The JetBrains plugin asks the same question the same way
 * (RepotoryClient.servesWebUi): deep links are best-effort in BOTH IDEs
 * (ADR-0035 decision 6), and the two must not drift apart on what counts as
 * "the UI is there".
 */
export async function servesWebUi(base: string): Promise<boolean> {
  return new Promise((resolve) => {
    const req = http.request(base.replace(/\/+$/, "") + "/", { method: "GET" }, (res) => {
      const type = String(res.headers["content-type"] ?? "").toLowerCase();
      resolve(res.statusCode === 200 && type.includes("text/html"));
      // Drain the body so the socket can be reused/closed cleanly.
      res.resume();
    });
    req.on("error", () => resolve(false));
    req.setTimeout(4000, () => {
      req.destroy();
      resolve(false);
    });
    req.end();
  });
}

/**
 * Parse the `repotory_wiki_search` tool result into page summaries.
 *
 * The result is a bare JSON array, like every other list tool (see
 * parseSearchHits) — the same tolerant parse applies.
 */
export function parseWikiPages(text: string): WikiPageSummary[] {
  const parsed = parseJsonLenient(text);
  if (!Array.isArray(parsed)) return [];
  return parsed as WikiPageSummary[];
}

/**
 * Parse the `repotory_wiki_read` tool result into one page.
 *
 * A shape check, not a repair pass: an unusable payload returns undefined and
 * the caller falls back to the matched excerpt, because a pane that shows
 * nothing is worse than one that shows the excerpt and says so.
 */
export function parseWikiRead(text: string): WikiPageDetail | undefined {
  const parsed = parseJsonLenient(text);
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return undefined;
  const page = parsed as Record<string, unknown>;
  const id = Number(page.id ?? 0);
  if (!id) return undefined;
  return {
    id,
    slug: typeof page.slug === "string" ? page.slug : "",
    title: typeof page.title === "string" && page.title ? page.title : `page ${id}`,
    kind: typeof page.kind === "string" ? page.kind : "",
    project_id: Number(page.project_id ?? 0),
    updated_at: typeof page.updated_at === "string" ? page.updated_at : "",
    content: typeof page.content === "string" ? page.content : "",
    source: typeof page.source === "string" ? page.source : "",
  };
}

export function excerpt(text: unknown, max = 90): string {
  if (typeof text !== "string") return "";
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat;
}

/** One project as the webview's project dropdown needs it. */
export type { ProjectOption };

/**
 * Parse the `repotory_projects_list` tool result into dropdown options.
 *
 * Same whole-document-first discipline as parseSearchHits: the tool returns
 * a bare JSON array, and a tolerant retry only runs when that fails.
 */
export function parseProjects(text: string): ProjectOption[] {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text.trim());
  } catch {
    return [];
  }
  if (!Array.isArray(parsed)) return [];
  const out: ProjectOption[] = [];
  for (const raw of parsed) {
    const obj = raw as Record<string, unknown> | null;
    const id = Number(obj?.id ?? 0);
    if (!id) continue;
    const name = typeof obj?.name === "string" && obj.name ? obj.name : `project ${id}`;
    out.push({ id, name });
  }
  return out;
}

/** The AI tidying proposal as the webview needs it: fields plus caveats. */
export interface OrganizeProposal {
  note_id: number;
  title: string;
  content: string;
  tags: string;
  warning?: string;
}

/**
 * Parse the `repotory_notes_organize` tool result. The service already
 * backfills an empty title from the stored note and clips fields to the
 * store's bounds, so this is a shape check, not a repair pass: an unusable
 * payload returns undefined and the caller shows the raw error instead of
 * opening an editor with half a draft.
 */
export function parseOrganizeProposal(text: string): OrganizeProposal | undefined {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text.trim());
  } catch {
    return undefined;
  }
  const obj = parsed as Record<string, unknown> | null;
  if (!obj || typeof obj !== "object") return undefined;
  const content = typeof obj.content === "string" ? obj.content : "";
  if (!content.trim()) return undefined;
  return {
    note_id: Number(obj.note_id ?? 0),
    title: typeof obj.title === "string" ? obj.title : "",
    content,
    tags: typeof obj.tags === "string" ? obj.tags : "",
    warning: typeof obj.warning === "string" && obj.warning ? obj.warning : undefined,
  };
}

// --------------------------------------------------------------- ask answers

/**
 * One source line of an `repotory_ask` answer.
 *
 * The tool writes it with Go's %q for the title, so the title may carry
 * backslash escapes ("a \"quoted\" title") and the id space is "page" or
 * "note" (service.EvidenceItem.Type). The shape is:
 *
 *     [P1] page "Some title" (id 42, slug some-slug, project Repotory)
 *     [N3] note "Another title" (id 7, project Repotory)
 *
 * The slug and the project name are optional; the id is not.
 */
const ASK_SOURCE_LINE = /^\[([A-Za-z]?\d+)\]\s+(\S+)\s+"((?:[^"\\]|\\.)*)"\s*\((.*)\)\s*$/;

/** The parenthesised tail: `id 42[, slug s][, project P]`. */
const ASK_SOURCE_META = /^id (\d+)(?:, slug ([^,]*))?(?:, project (.*))?$/;

/**
 * The trailing note the tool appends when the context budget dropped
 * matches. It is an annotation, not a source — and it must not be mistaken
 * for a malformed one.
 */
const ASK_OMITTED_LINE = /^\(\d+ further match\(es\) omitted[^)]*\)$/;

/** The marker the tool puts between the reply and the source list. */
const ASK_SOURCES_MARKER = "\n\nSources:\n";

/**
 * Parse the `repotory_ask` answer into the reply and its sources.
 *
 * The tool answers in TEXT (ADR-0033): a reply, then a source list. The
 * marker is searched from the END, because a reply may legitimately contain
 * the word "Sources:" — the real list is always the last one.
 *
 * A source line that does not match the contract means the contract changed
 * (or the model wrote into the list). The whole text is then returned
 * UNPARSED, so the caller shows it verbatim: half a source list is worse
 * than no parsing, because the reader cannot tell what was dropped.
 */
export function parseAskAnswer(text: string): AskAnswer {
  const raw = text ?? "";
  const at = raw.lastIndexOf(ASK_SOURCES_MARKER);
  if (at < 0) {
    // No source list at all: a keyword fallback, or a reply with no evidence.
    // That is a complete answer, not a parse failure.
    return { answer: raw.trim(), raw, sources: [], parsed: true };
  }
  const answer = raw.slice(0, at).trim();
  const sources: EvidenceSource[] = [];
  for (const line of raw.slice(at + ASK_SOURCES_MARKER.length).split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    if (ASK_OMITTED_LINE.test(trimmed)) continue;
    const match = ASK_SOURCE_LINE.exec(trimmed);
    if (!match) return { answer: "", raw, sources: [], parsed: false };
    const meta = ASK_SOURCE_META.exec(match[4]?.trim() ?? "");
    if (!meta) return { answer: "", raw, sources: [], parsed: false };
    sources.push({
      ref: match[1] ?? "",
      type: match[2] ?? "",
      title: unquoteGo(match[3] ?? ""),
      id: Number(meta[1]),
      slug: meta[2] ? meta[2] : undefined,
      project: meta[3] ? meta[3] : undefined,
    });
  }
  return { answer, raw, sources, parsed: true };
}

/**
 * Undo Go's %q quoting of a source title.
 *
 * The service writes titles with %q, so a title with a quote, a backslash or
 * a newline arrives escaped. Anything this does not recognise is kept
 * verbatim (backslash included) rather than dropped.
 */
export function unquoteGo(text: string): string {
  if (!text.includes("\\")) return text;
  let out = "";
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (ch !== "\\") {
      out += ch;
      continue;
    }
    const next = text[i + 1];
    i++;
    switch (next) {
      case "n":
        out += "\n";
        break;
      case "t":
        out += "\t";
        break;
      case "r":
        out += "\r";
        break;
      case "a":
        out += "\x07";
        break;
      case "b":
        out += "\b";
        break;
      case "f":
        out += "\f";
        break;
      case "v":
        out += "\x0b";
        break;
      case "\\":
        out += "\\";
        break;
      case '"':
        out += '"';
        break;
      case "x": {
        const hex = text.slice(i + 1, i + 3);
        if (/^[0-9a-fA-F]{2}$/.test(hex)) {
          out += String.fromCharCode(parseInt(hex, 16));
          i += 2;
        } else {
          out += "\\x";
        }
        break;
      }
      case "u": {
        const hex = text.slice(i + 1, i + 5);
        if (/^[0-9a-fA-F]{4}$/.test(hex)) {
          out += String.fromCharCode(parseInt(hex, 16));
          i += 4;
        } else {
          out += "\\u";
        }
        break;
      }
      default:
        out += next === undefined ? "\\" : "\\" + next;
        break;
    }
  }
  return out;
}