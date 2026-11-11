// The direct probes: same contract as the server's ListAIModels / TestAIChat
// (internal/service/ai.go), so a wrong endpoint is diagnosed identically no
// matter which surface asked. fetch is swapped per test — the interesting
// behaviour is WHICH url gets tried next, and that is decided by status codes.
import { strict as assert } from "node:assert";
import { test, type TestContext } from "node:test";
import { aiCandidateUrls, parseProbeModels, probeChat, probeModels } from "../aiProbe.js";

type FetchLike = (url: string | URL | Request, init?: RequestInit) => Promise<Response>;

/** Swap globalThis.fetch for one test, restore afterwards. */
async function withFetch(t: TestContext, impl: FetchLike, fn: () => Promise<void>): Promise<void> {
  const original = globalThis.fetch;
  globalThis.fetch = impl as typeof globalThis.fetch;
  t.after(() => {
    globalThis.fetch = original;
  });
  await fn();
}

function jsonRes(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status });
}

test("candidate URLs mirror the server's near-miss shapes", () => {
  assert.deepEqual(aiCandidateUrls("https://api.deepseek.com", "/models"), [
    "https://api.deepseek.com/models",
    "https://api.deepseek.com/v1/models",
  ]);
  // ".../api/v1" is the classic mistype: also try the server root's /v1.
  assert.deepEqual(aiCandidateUrls("http://localhost:8080/api/v1/", "/models"), [
    "http://localhost:8080/api/v1/models",
    "http://localhost:8080/v1/models",
  ]);
  // An explicit /v1 must not gain a doubled variant.
  assert.deepEqual(aiCandidateUrls("http://localhost:1234/v1", "/models"), [
    "http://localhost:1234/v1/models",
  ]);
});

test("model parsing reads the OpenAI shape and Ollama's native one", () => {
  assert.deepEqual(parseProbeModels(JSON.stringify({ data: [{ id: "b" }, { id: "a" }] })), ["a", "b"]);
  assert.deepEqual(parseProbeModels(JSON.stringify({ models: [{ name: "qwen" }] })), ["qwen"]);
  assert.deepEqual(parseProbeModels("not json"), []);
});

test("probeModels walks candidates on 404 and adopts the base that answered", async (t) => {
  const seen: string[] = [];
  await withFetch(
    t,
    async (url) => {
      seen.push(String(url));
      if (String(url) === "https://api.x.com/models") return jsonRes(404, { error: "not found" });
      return jsonRes(200, { data: [{ id: "m2" }, { id: "m1" }] });
    },
    async () => {
      const result = await probeModels("https://api.x.com", "k");
      assert.deepEqual(seen, ["https://api.x.com/models", "https://api.x.com/v1/models"]);
      assert.deepEqual(result.models, ["m1", "m2"]);
      assert.equal(result.resolvedBaseUrl, "https://api.x.com/v1");
    },
  );
});

test("probeModels stops on an auth failure instead of probing made-up siblings", async (t) => {
  const seen: string[] = [];
  await withFetch(
    t,
    async (url) => {
      seen.push(String(url));
      return jsonRes(401, { error: { message: "bad key" } });
    },
    async () => {
      await assert.rejects(probeModels("https://api.x.com", "wrong"), /auth failed \(401\)/);
      assert.deepEqual(seen, ["https://api.x.com/models"]);
    },
  );
});

test("probeModels reports a network refusal as 'nothing is listening'", async (t) => {
  await withFetch(
    t,
    async () => {
      throw new Error("connect ECONNREFUSED 1.2.3.4:443");
    },
    async () => {
      await assert.rejects(probeModels("https://api.x.com", ""), /nothing is listening/);
    },
  );
});

test("probeChat extracts the reply and the resolved base", async (t) => {
  await withFetch(
    t,
    async () => jsonRes(200, { choices: [{ message: { content: " OK " }, finish_reason: "stop" }] }),
    async () => {
      const result = await probeChat("https://api.x.com/v1", "m", "k");
      assert.equal(result.reply, "OK");
      assert.equal(result.resolvedBaseUrl, "https://api.x.com/v1");
    },
  );
});

test("probeChat reports a 200 error envelope in the provider's own words", async (t) => {
  await withFetch(
    t,
    async () => jsonRes(200, { error: { message: "provider_unavailable — retry later" } }),
    async () => {
      await assert.rejects(probeChat("https://api.x.com/v1", "m", "k"), /provider_unavailable/);
    },
  );
});
