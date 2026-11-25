// Direct probes for the AI provider sheet: the model list and a minimal chat
// completion, sent by the EXTENSION HOST straight at the configured endpoint.
//
// Why direct: the sheet's other actions (read config, save, "test the saved
// provider") go through the local Cinder service's /api/rpc bridge, because
// the settings and the stored key live in the service's database. But "does
// this endpoint answer" is a property of the ENDPOINT, not of the service —
// and the service is often NOT running while the user is configuring the
// endpoint (it comes up with the desktop app). Routing these two probes
// through the bridge made the sheet answer "ECONNREFUSED 127.0.0.1:18765" for
// a perfectly reachable OpenRouter.
//
// Same contract as the server's ListAIModels / TestAIChat
// (internal/service/ai.go), so the IDEs and the desktop app diagnose an
// endpoint identically: same candidate URLs, same 404-means-try-next rule,
// same payload shapes, same hints.

/** How long a probe may wait: a model list is fast, a completion is not. */
const PROBE_TIMEOUT_MS = 30_000;

export interface ProbeModelsResult {
  models: string[];
  /** The candidate base that actually answered, so the UI can adopt it. */
  resolvedBaseUrl: string;
}

export interface ProbeChatResult {
  reply: string;
  resolvedBaseUrl: string;
}

/**
 * The near-miss base shapes worth trying, mirroring aiCandidateURLs:
 * LM Studio's base is "/v1" but ".../api/v1" is the classic mistype, and
 * ".../api" plus the path is LM Studio's own alternate layout.
 */
export function aiCandidateUrls(rawBase: string, path: string): string[] {
  const base = rawBase.trim().replace(/\/+$/, "");
  const urls = [base + path];
  if (!base.endsWith("/v1")) urls.push(base + "/v1" + path);
  if (base.endsWith("/api/v1")) urls.push(base.slice(0, -"/api/v1".length) + "/v1" + path);
  if (base.endsWith("/api")) urls.push(base.slice(0, -"/api".length) + path);
  return [...new Set(urls)];
}

/** The Authorization header only when there is something to authorize with. */
function authHeaders(apiKey: string): Record<string, string> {
  const key = apiKey.trim();
  return key ? { Authorization: `Bearer ${key}` } : {};
}

async function fetchWithTimeout(url: string, init: RequestInit, timeoutMs: number): Promise<Response> {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), timeoutMs);
  try {
    return await fetch(url, { ...init, signal: ctl.signal });
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Parse a /models body: the OpenAI shape ({"data":[{"id"}]}) first, then
 * Ollama's native ({"models":[{"name"}]}). Sorted like the server sorts them.
 */
export function parseProbeModels(raw: string): string[] {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  if (!parsed || typeof parsed !== "object") return [];
  const obj = parsed as Record<string, unknown>;
  const ids: string[] = [];
  if (Array.isArray(obj.data)) {
    for (const m of obj.data as Array<Record<string, unknown>>) {
      if (typeof m?.id === "string" && m.id) ids.push(m.id);
    }
  }
  if (!ids.length && Array.isArray(obj.models)) {
    for (const m of obj.models as Array<Record<string, unknown>>) {
      if (typeof m?.name === "string" && m.name) ids.push(m.name);
    }
  }
  return [...new Set(ids)].sort();
}

/** The provider's own error words, when a 200 body carries an error envelope. */
function providerErrorMessage(raw: string): string | undefined {
  try {
    const obj = JSON.parse(raw) as { error?: { message?: unknown } | unknown };
    const msg = (obj as { error?: { message?: unknown } })?.error;
    if (msg && typeof msg === "object" && typeof (msg as { message?: unknown }).message === "string") {
      const text = (msg as { message: string }).message.trim();
      return text || undefined;
    }
  } catch {
    /* not JSON — fall through */
  }
  return undefined;
}

function networkHint(error: unknown): string {
  const msg = error instanceof Error ? error.message : String(error);
  if (/ECONNREFUSED|fetch failed|NetworkError/i.test(msg)) {
    return `nothing is listening on that host:port — is the server running, and is the port right? (${msg})`;
  }
  if (/abort/i.test(msg)) return "connection timed out";
  if (/ENOTFOUND|getaddrinfo/i.test(msg)) return "host not found — check the address";
  return `AI endpoint unreachable: ${msg}`;
}

function truncated(text: string, max: number): string {
  return text.length > max ? text.slice(0, max) : text;
}

/**
 * Turn a raw error body into something a human can act on. Gateways and
 * misrouted requests frequently answer with a whole HTML page (Vercel's
 * __next_error__ 404 page, a router's error template) — pasting 300
 * characters of markup into the status line helped nobody. An HTML body is
 * summarized to one line naming the likely causes; JSON and plain-text
 * bodies keep the provider's own words. Mirrors aiclient.BodySummary.
 */
function bodySummary(text: string): string {
  const t = text.trim();
  if (/^<!doctype html|^<html/i.test(t)) {
    return "(an HTML page, not an API answer — usually a provider risk-control interception of non-browser requests, a proxy/gateway detour, or a wrong path; it was retried once — if it keeps failing, check the Base URL, any local proxy, or try a different network)";
  }
  return truncated(t, 300);
}

/**
 * Is this response a wrong-path answer worth another candidate URL?
 *
 * A 404 always is — the classic wrong-shape probe. A 400 whose body is the
 * relay-gateway rejection ("Request path cannot be relayed" — CF-Worker-style
 * proxies that only forward /v1/*) is the same dead end: the path itself is
 * unsupported, so the sibling candidate may be the right one. Any other 400
 * is about the REQUEST (bad model, bad body) and must surface to the user
 * unchanged. Mirrors aiclient.PathRejected so the IDEs and the desktop app
 * walk candidate URLs identically.
 */
function pathRejected(status: number, body: string): boolean {
  if (status === 404) return true;
  return status === 400 && body.toLowerCase().includes("request path cannot be relayed");
}

/**
 * Probe the endpoint's /models with the typed values — no service, no save.
 *
 * Only a rejected path (404, or the relay-gateway 400 — see pathRejected)
 * moves to the next candidate; an auth failure or any other status is a real
 * answer about the endpoint and must not be retried against a made-up
 * sibling URL — the same rule the server applies.
 */
export async function probeModels(rawBase: string, apiKey: string): Promise<ProbeModelsResult> {
  const base = rawBase.trim();
  if (!base) throw new Error("base URL is required");
  const candidates = aiCandidateUrls(base, "/models");

  let lastNet: unknown;
  let lastStatus = 0;
  let lastBody = "";
  for (const url of candidates) {
    let res: Response;
    try {
      res = await fetchWithTimeout(url, { headers: authHeaders(apiKey) }, PROBE_TIMEOUT_MS);
    } catch (err) {
      lastNet = err;
      continue;
    }
    let text = await res.text().catch(() => "");
    // Transient-flavour rejection (404 page / relay 400): retry the SAME url
    // once after a short backoff — provider risk-control windows and edge
    // blips are exactly the failure a single retry absorbs. Probe traffic is
    // side-effect free so the retry can never double-bill. Mirrors
    // aiclient.probeGet.
    if (pathRejected(res.status, text)) {
      await new Promise((r) => setTimeout(r, 1200));
      try {
        res = await fetchWithTimeout(url, { headers: authHeaders(apiKey) }, PROBE_TIMEOUT_MS);
        text = await res.text().catch(() => "");
      } catch (err) {
        lastNet = err;
        continue;
      }
    }
    if (pathRejected(res.status, text)) {
      lastStatus = res.status;
      lastBody = text;
      continue;
    }
    if (res.status === 401 || res.status === 403) {
      throw new Error(`auth failed (${res.status}) — check the API key`);
    }
    if (res.status !== 200) {
      throw new Error(`AI endpoint returned ${res.status}: ${bodySummary(text)}`);
    }
    const models = parseProbeModels(text);
    if (!models.length) throw new Error("endpoint returned no models");
    return { models, resolvedBaseUrl: url.slice(0, -"/models".length) };
  }
  if (lastNet !== undefined) throw new Error(networkHint(lastNet));
  throw new Error(
    `no /models path found — tried: ${candidates.join(", ")} (last status ${lastStatus}: ${bodySummary(lastBody)})`,
  );
}

/**
 * Verify a specific model answers a minimal completion — the direct twin of
 * TestAIChat. A 200 body can still carry an error envelope instead of a
 * completion (OpenRouter does this when the upstream provider is saturated),
 * so the provider's own words are reported rather than a bare "no choices".
 */
export async function probeChat(rawBase: string, model: string, apiKey: string): Promise<ProbeChatResult> {
  const base = rawBase.trim();
  if (!base) throw new Error("base URL is required");
  if (!model.trim()) throw new Error("model is required");
  const candidates = aiCandidateUrls(base, "/chat/completions");
  const body = JSON.stringify({
    model: model.trim(),
    messages: [{ role: "user", content: "Reply with exactly: OK" }],
  });

  let lastNet: unknown;
  let lastStatus = 0;
  let lastBody = "";
  for (const url of candidates) {
    let res: Response;
    try {
      res = await fetchWithTimeout(
        url,
        {
          method: "POST",
          headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
          body,
        },
        PROBE_TIMEOUT_MS,
      );
    } catch (err) {
      lastNet = err;
      continue;
    }
    let text = await res.text().catch(() => "");
    // Same transient-rejection retry as probeModels (mirrors aiclient.probePost):
    // probe bodies are the fixed "Reply with exactly: OK", so re-sending is safe.
    if (pathRejected(res.status, text)) {
      await new Promise((r) => setTimeout(r, 1200));
      try {
        res = await fetchWithTimeout(
          url,
          {
            method: "POST",
            headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
            body,
          },
          PROBE_TIMEOUT_MS,
        );
        text = await res.text().catch(() => "");
      } catch (err) {
        lastNet = err;
        continue;
      }
    }
    if (pathRejected(res.status, text)) {
      lastStatus = res.status;
      lastBody = text;
      continue;
    }
    if (res.status === 401 || res.status === 403) {
      throw new Error(`auth failed (${res.status}) — check the API key`);
    }
    if (res.status !== 200) {
      throw new Error(`AI endpoint returned ${res.status}: ${bodySummary(text)}`);
    }
    let parsed: {
      choices?: Array<{ message?: { content?: string } }>;
      error?: unknown;
    } | null = null;
    try {
      parsed = JSON.parse(text) as { choices?: Array<{ message?: { content?: string } }>; error?: unknown } | null;
    } catch {
      throw new Error(`AI endpoint returned malformed JSON: ${truncated(text, 200)}`);
    }
    const choices = parsed?.choices ?? [];
    if (!choices.length) {
      const providerMessage = providerErrorMessage(text);
      if (providerMessage) throw new Error(providerMessage);
      throw new Error("AI endpoint returned no choices");
    }
    const reply = (choices[0]?.message?.content ?? "").trim();
    return { reply, resolvedBaseUrl: url.slice(0, -"/chat/completions".length) };
  }
  if (lastNet !== undefined) throw new Error(networkHint(lastNet));
  throw new Error(
    `no working path found — tried: ${candidates.join(", ")} (last status ${lastStatus}: ${bodySummary(lastBody)})`,
  );
}
