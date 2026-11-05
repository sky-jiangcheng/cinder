// Unit tests for the pure parsers the chat console depends on.
//
// The ask parser is the risky one: it reads the SOURCE LIST back out of a
// TEXT answer, and its failure mode must be "show the raw text", never "show
// half a source list". These tests pin that, plus the exact wire shapes the
// Go service produces (cmd/mcp/tools_ask.go's formatEvidenceAnswer).
//
// Run with: npm test  (node --test over the compiled output)
import { strict as assert } from "node:assert";
import { test } from "node:test";
import { parseAskAnswer, parseSearchHits, parseWikiPages, unquoteGo } from "../headless.js";

test("an answer with sources splits into the reply and the source list", () => {
  const text = [
    "The 503 retry lives in the gateway, not in the client.",
    "",
    "Sources:",
    '[P1] page "Retry policy" (id 42, slug retry-policy, project Cinder)',
    '[N3] note "503 notes" (id 7, project Cinder)',
    "",
  ].join("\n");
  const parsed = parseAskAnswer(text);
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.answer, "The 503 retry lives in the gateway, not in the client.");
  assert.equal(parsed.sources.length, 2);
  assert.deepEqual(parsed.sources[0], {
    ref: "P1",
    type: "page",
    title: "Retry policy",
    id: 42,
    slug: "retry-policy",
    project: "Cinder",
  });
  assert.deepEqual(parsed.sources[1], {
    ref: "N3",
    type: "note",
    title: "503 notes",
    id: 7,
    slug: undefined,
    project: "Cinder",
  });
});

test("a source without a slug or a project still parses", () => {
  const parsed = parseAskAnswer('Answer.\n\nSources:\n[P1] note "Plain" (id 7)\n');
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.sources.length, 1);
  assert.equal(parsed.sources[0]?.slug, undefined);
  assert.equal(parsed.sources[0]?.project, undefined);
  assert.equal(parsed.sources[0]?.id, 7);
});

test("the budget annotation is not a source and does not break the parse", () => {
  const text = [
    "Answer.",
    "",
    "Sources:",
    '[P1] note "a" (id 1)',
    "(3 further match(es) omitted by the context budget)",
    "",
  ].join("\n");
  const parsed = parseAskAnswer(text);
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.sources.length, 1);
});

test("an answer without a source list is a complete answer, not a failure", () => {
  const parsed = parseAskAnswer("No results found for: retry");
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.answer, "No results found for: retry");
  assert.equal(parsed.sources.length, 0);
});

test("a malformed source line falls back to the raw text", () => {
  // The contract changed (or the model wrote into the list): showing half a
  // source list would hide WHICH sources were dropped.
  const text = 'Answer.\n\nSources:\n[P1] note "unterminated (id 1)\n';
  const parsed = parseAskAnswer(text);
  assert.equal(parsed.parsed, false);
  assert.equal(parsed.answer, "");
  assert.equal(parsed.sources.length, 0);
  assert.equal(parsed.raw, text);
});

test("the LAST Sources marker wins when the reply mentions the word", () => {
  const text = 'See Sources:\n\nSources:\n[P1] note "x" (id 1)\n';
  const parsed = parseAskAnswer(text);
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.answer, "See Sources:");
  assert.equal(parsed.sources.length, 1);
});

test("a quoted title is unquoted the way Go's %q wrote it", () => {
  const text = 'Answer.\n\nSources:\n[P1] note "say \\"hi\\"" (id 5, project X)\n';
  const parsed = parseAskAnswer(text);
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.sources[0]?.title, 'say "hi"');
});

test("an empty answer is not a crash", () => {
  const parsed = parseAskAnswer("");
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.answer, "");
  assert.equal(parsed.sources.length, 0);
});

test("unquoteGo keeps unknown escapes verbatim instead of dropping them", () => {
  assert.equal(unquoteGo("plain"), "plain");
  assert.equal(unquoteGo("a\\nb"), "a\nb");
  assert.equal(unquoteGo("a\\tb"), "a\tb");
  // 'say \"hi\"' is how Go's %q writes a title that contains quotes.
  assert.equal(unquoteGo('say \\"hi\\"'), 'say "hi"');
  assert.equal(unquoteGo("back\\\\slash"), "back\\slash");
  assert.equal(unquoteGo("weird \\q"), "weird \\q");
});

test("the search parser still reads the bare JSON array the tool returns", () => {
  const hits = parseSearchHits('[{"type":"note","id":1,"project_id":7,"title":"T","snippet":"S"}]');
  assert.equal(hits.length, 1);
  assert.equal(hits[0]?.title, "T");
  assert.equal(hits[0]?.project_id, 7);
});

test("the wiki parser still reads the bare JSON array the tool returns", () => {
  const pages = parseWikiPages('[{"id":3,"slug":"s","title":"P","kind":"concept","project_id":0}]');
  assert.equal(pages.length, 1);
  assert.equal(pages[0]?.slug, "s");
  assert.equal(pages[0]?.project_id, 0);
});
