// The card's actions, as the webview ships them.
//
// A card is a pointer, not a destination: its body already opens in place, so
// the action set must not contain a way OUT of the panel. What it must contain
// is a way to ask about what was read — and the click has to ASK, not merely
// fill the composer: a button labelled "Ask about this" that waits for Enter
// makes the label a promise it does not keep.
//
// These assertions read the generated script because the webview has no DOM
// here; they pin the contract (the labels and the message types), which is the
// part the host and the two IDE surfaces have to agree on.
import { test } from "node:test";
import assert from "node:assert/strict";

import { consoleHtml } from "../chatConsoleHtml.js";

test("a card offers a follow-up, and nothing that leaves the panel", () => {
  const html = consoleHtml("test-nonce");

  assert.match(html, /Ask about this/);
  assert.doesNotMatch(html, /Open in Cinder/);
  // The host has no "open" message any more: a card addressed it by leaving.
  assert.doesNotMatch(html, /type: 'open'/);
  // The question goes into the composer (so the turn records what was asked)
  // AND is submitted by the same path Enter uses — in one click.
  assert.match(html, /function followUp\(card\)/);
  const ask = /function askNow\(text\) \{([\s\S]*?)\n\}/.exec(html)?.[1] ?? "";
  assert.match(ask, /setMode\('ask'\)/, "the follow-up must run as an Ask turn");
  assert.match(ask, /q\.value = text/, "the composer must show what was asked");
  assert.match(ask, /submit\(\)/, "the click must send, not just fill the composer");
  // Both question clicks go through it: the card's, and the idle example's.
  const about = /function askAbout\(card\) \{([\s\S]*?)\n\}/.exec(html)?.[1] ?? "";
  assert.match(about, /askNow\(followUp\(card\)\)/);
  assert.match(html, /askNow\(btn\.textContent\.trim\(\)\)/);
});

test("the drafted question names the item so the answer is about THAT one", () => {
  const html = consoleHtml("test-nonce");

  // Same wording as the JetBrains panel's followUpQuestion (ADR-0036
  // decision 8): the same card asked about on two IDEs must be the same
  // question, or the two panels are two products.
  assert.match(html, /'Tell me about ' \+ kind \+ ' #' \+ card\.id/);
  assert.match(html, /what it says, and why it matters\./);
  // A quoted title cannot end the phrase early.
  assert.match(html, /replace\(\/"\/g, "'"\)|replace\(\/"\/g, '\\''\)/);
});
