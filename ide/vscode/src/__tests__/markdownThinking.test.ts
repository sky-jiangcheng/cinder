// The answer, not the model's notes — and the notes still readable.
//
// A gateway that does not separate reasoning_content leaves the reasoning
// inline in content, wrapped in <think>…</think>. The webview splits it out
// and folds it behind a "Thinking" row (the shape CodeBuddy and the JetBrains
// pane use). The webview has no DOM here, so this test lifts splitThinking out
// of the generated script and runs it: the behaviour under test is the regular
// expressions, and pinning their text would happily pass while the pane still
// showed the notes as prose.
import { test } from "node:test";
import assert from "node:assert/strict";

import { consoleHtml } from "../chatConsoleHtml.js";

const html = consoleHtml("test-nonce");
const body = /function splitThinking\(src\) \{([\s\S]*?)\n\}/.exec(html)?.[1];
if (!body) throw new Error("splitThinking not found in the webview script");
const splitThinking = new Function("src", body) as (src: string) => { thoughts: string[]; body: string };

test("a closed reasoning block is split out of the answer", () => {
  const split = splitThinking("<think>plan: read the note first</think>The retry budget was raised.");
  assert.deepEqual(split.thoughts, ["plan: read the note first"]);
  assert.equal(split.body, "The retry budget was raised.");
});

test("both spellings and any case are recognised", () => {
  assert.deepEqual(splitThinking("<thinking>x</thinking>Answer.").thoughts, ["x"]);
  assert.deepEqual(splitThinking("<THINK>x</THINK>Answer.").thoughts, ["x"]);
  assert.deepEqual(splitThinking("<reasoning>x</reasoning>Answer.").thoughts, ["x"]);
});

test("an unterminated block is folded as reasoning, never as the answer", () => {
  // Streaming delivers the open tag long before the close: the notes must not
  // flash in as the reply while the model is still thinking.
  const split = splitThinking("Answer so far.\n<think>still thinking");
  assert.deepEqual(split.thoughts, ["still thinking"]);
  assert.equal(split.body, "Answer so far.");
});

test("a stray tag is removed, the text around it is kept", () => {
  assert.equal(splitThinking("Answer.</think>").body, "Answer.");
});

test("the pane folds the notes behind a Thinking row", () => {
  // The row carries the state (expandedThoughts), like the Sources list; the
  // notes themselves render through md() as their own block.
  assert.match(html, /'Thinking ' \+ \(isOpen \? '▾' : '▸'\)/);
  assert.match(html, /notes\.className = 'thinking'/);
  assert.match(html, /expandedThoughts\.add\(turn\.id\)/);
});
