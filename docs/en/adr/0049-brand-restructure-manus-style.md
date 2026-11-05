# ADR-0049: Rebuild the brand the Manus way — a new parent brand, an independent name per surface, the identifier layer frozen

Status: **Accepted (2026-11-05, the user picked option (c) "full Manus-style restructure" among the four routes offered for "how do we redefine the brand and the UI"; the three words are still to be picked)**
Date: 2026-11-05
Related: [ADR-0037](../../docs/en/adr/0037-brand-legibility-and-ui-language.md) (brand legibility and UI language — this ADR **narrowed the scope of its decision 4**), [ADR-0047](../../docs/en/adr/0047-resident-agent-separate-host.md) (two products, one job each), [ADR-0048](../../docs/en/adr/0048-core-contract-version.md) (the contract version), [ADR-0034](../../docs/en/adr/0034-desktop-three-column-shell.md) (the precedent for separating the display name from the binary name), [ADR-0006](../../docs/en/adr/0006-scope-freeze.md) (scope freeze and frozen identifiers)
Starting schema: v28 (**this ADR does not change the schema**)

## Background

### Why: one question that exposed a structural problem

The user asked: "**Is the brand name Repotory really any good? Is the current UI design really any good? Think about it again with Manus in mind.**"

That is not a request to "swap the name" — it is a structural check-up. The investigation turned up six interlocking facts, and together they collapse the subjective question "is the name good?" into a decidable structural one: **the word "Repotory" refers to three different things at once, while the repo's own prose had already assigned each of them a role.**

### Fact one: the repo already ran a brand diagnosis — and it explicitly dodged the name half of the question

[ADR-0037](../../docs/en/adr/0037-brand-legibility-and-ui-language.md) decision 4 says:

> **The brand name `Repotory` stays**: it is a frozen identifier (ADR-0006). The "legibility" problem is in the **visual construct**, not in the name.

That is not a brush-off — ADR-0037's diagnosis is solid (the "R" on the master art is the most generic letter in the world, the hairline thins to 0.46px at 26px, the UI tokens were still Tailwind blue while the master art is warm cream). But it answers **a different question**: at the time the user raised "legibility is low" (visual), and today the question is "is the name good" (the word). **Those are two faces of one thing, and that conclusion covered only one of them.** More importantly, it wrote "the brand name does not change" as a **decision** rather than as an **item awaiting validation** — so today's new question cannot overturn it, only be supplemented by it.

### Fact two: the prose already drew the layers; the product names never caught up

| Role | The wording the repo already uses | What the product names say |
|---|---|---|
| Engine / memory layer | First screen: "The local-first memory layer for AI coding agents" ([README.md:8](../../README.md)); the artifact table calls `repotory-mcp` the "**Core agent memory engine**" ([README.md:64](../../README.md)) | `repotory-mcp` ✓ |
| Desktop workbench | The artifact table calls it "**Repotory Desktop**" ([README.md:65](../../README.md)) | `wails.json`'s `productName` is "**Repotory**" — **the surface shares its name with the engine** |
| Second product | — | "**Repotory Agent**" (the `repotory-agent` repo's `tauri.conf.json`) — **a qualifier-suffix name** |

In other words: **the name "Repotory Desktop" exists only in the README; the app itself does not use it.** That is a live inconsistency independent of any word choice — **it has to be fixed whichever route is taken**.

### Fact three: what is transferable from Manus is not "a better word"

Manus' structure since 2026-09-28 is: **the parent brand (Manus) = the platform/engine; the first surface = Manus Studio (parent brand + a role qualifier); the second surface = Cue (a completely different word — the store listing is "Cue by Manus")**.

The key point: **the engine never got a name of its own; the names went to the surfaces.** We are the mirror image of that: "Repotory" is both the engine and the first surface, and the second surface is called "Repotory Agent" — which literally means "the agent that serves Repotory", inherently an accessory rather than an independent product. **Manus solved exactly this wall with a standalone word (Cue) — the same wall our Agent hits.**

### Fact four: the memory words whose meaning is obvious are all taken in this very niche

The first instinct when picking a name is to take a word that "means memory". The availability check turned that entire class down:

| Candidate | What it collides with |
|---|---|
| **Engram** (the physical trace of a memory, in neuroscience) | `Gentleman-Programming/engram` — **a one-line match with our positioning**: "a standalone tool you can use with any MCP-compatible agent", a persistent memory layer; plus Engram Enterprise by Sphere Inc (enterprise persistent AI memory) |
| **Trove** (a chest of precious things) | `Lowside-Labs/Trove` — "turn your saved web into a local … then point Claude Code / Codex at it and ask" — **the same story** |
| **Cairn** (a cairn of waymarking stones) | ① `Cairn: A Local Tool for AI Agent Architecture Memory` (Reddit, ClaudeWorkflows) — **same niche again**; ② the open-source AI penetration-testing tool Cairn, which made Reuters for being used at scale by hackers |

**That fact changes the picking strategy**: in AI-memory, **the obvious words are already taken** — because everyone is looking at the same shortlist. Only two routes remain: ① pick a **real word whose meaning is oblique but whose shape is distinctive** (buying distinctiveness at the cost of one explanation); ② pick a **word borrowed from another trade** (like Manus' "by hand" or Cue's "give it a cue" — **the meaning comes from a story, not from a definition**).

### Fact five: the repo has already been renamed three times; the migration handles one generation at a time

The package comment in `internal/platform/platform.go` records the data directory's history: **gitboard → gitbuddy → reponest → repotory**; `dirName = "repotory"`, `legacyDirName = "reponest"`, and the comment states plainly that "**One generation back is handled per migration, exactly as each earlier rename did in its day**".

So one fact has to go into the decision: **this is the fourth rename, and the data-directory migration has to be written one more generation** — users who skipped a generation (whose directory is older than the previous one) lose their data directory. At the same time, **this round cannot be another half-applied rename**: ADR-0037's background records this brand's history of "four half-landed rebrands" (the activity bar, the tool window, the favicon, the README each missed one).

### Fact six: the identifier layer and the display layer are two layers, and the repo has separated them once

[README.md:221](../../README.md) lists the frozen identifiers: `repotory` = binary name, user data directory, MCP server `repotory-mcp`, tool prefix `repotory_*`. And [ADR-0034](../../docs/en/adr/0034-desktop-three-column-shell.md) decision 6 already performed **a display-layer-only rename**: `wails.json`'s `name` became `Repotory` (while `outputfilename` stayed `repotory`) — **the binary name `repotory` is a frozen identifier; only the bundle directory changed**.

**So "renaming is expensive" is an illusion**: what is expensive is wiping the brand assets (the master art plus ADR-0037 decision 1's "recitable construct" have to be re-verified once), not the migration. The migration only touches the display layer.

## Decision

### ① Take route C: a full display-layer restructure

**The display layer of the brand and the repos is restructured wholesale**: the parent brand gets a new word, the desktop workbench and the Agent each get their own name, and both repos' display names plus the documentation site's navigation move in the same batch.

**The routing layer (tool prefix, binary name, data directory, MCP server name) does not move** (rationale in decision ③).

### ② Three slots, each with its own place

| Slot | What it is | Naming form |
|---|---|---|
| **Parent brand** | the engine / memory layer (the thing behind `repotory-mcp`) | one standalone word |
| **Desktop workbench** | the Wails desktop app (browse, edit, approve, govern) | parent brand + a role qualifier (the Manus Studio form) **or** another standalone word |
| **Agent** | the separate repo's copilot (read, chat, execute) | **a completely different word** (the Cue form) — **no longer "X Agent"** |

**The qualifier-suffix form "X Agent" is rejected**: it reads the second product as an accessory of the first — while ADR-0047 decision ③ already defines the Agent as an **independent product** (separate repo, separate stack, its own tray for recall). **A name must not be older than the decision it encodes.**

### ③ The identifier layer is frozen: the tool prefix, binary name, data directory and MCP server name stay exactly as they are

`repotory_*` / `repotory` / `repotory-mcp` / `repotory-server` / `~/…/repotory/repotory.db` all **stay**. Three reasons:

1. **They are interfaces, not brands** — changing them breaks every user's `claude mcp add` configuration, IDE plugin allowlists and hook scripts; the only thing bought in exchange is a different word on the command line.
2. **ADR-0006's freeze discipline still stands**, and ADR-0034 already set the precedent of "display layer only".
3. **If they ever do move, the correct route is a contract major bump** (the semantics ADR-0048 just established: major = breaking) — **but that belongs to the day there is a second real consumer**. On a personal tool whose user count is still in single digits, manufacturing a migration while there are zero users is discipline applied where it buys nothing.

**The cost must be stated**: after the rename, **the brand name and the name on the command line will disagree** (the brand is the new word, the binary is still `repotory-mcp`). That is **deliberate**: once an interface name ships, it is somebody else's dependency, and renaming it is a breaking change.

### ④ The repos' display names and the documentation site's navigation move in the same batch

Both repos (`sky-jiangcheng/repotory` and `repotory-agent`): **display names**, README first screens, the documentation site's home page and sidebar, the IDE plugins' `displayName` and command titles, the Homebrew cask `name`, the install script's wording — **all changed in one batch**.

**Renaming the GitHub repository slugs**: GitHub redirects old URLs automatically, so this **is doable**, but it leaves old clones needing a manual `git remote` fix — **that has to go into the release notes**.

**History is not rewritten**: old names inside ADRs and the CHANGELOG are history, and by repo convention they stay as they are (**only the current surface changes, not the historical one**).

### ⑤ Eight hard constraints on any candidate word

A candidate must satisfy all of these:

1. **Not shaped like `repository` / `repotory`** (the old word's problem must not recur);
2. **No political meaning and no ethnic or regional taboo in English**;
3. **Two or three syllables, spellable after hearing it once** (the stress must be unambiguous);
4. **At least one TLD other than `.com` registrable**, and the GitHub org / username available;
5. **No same-named package on npm / crates.io / PyPI** (this repo ships `repotory-init`, the agent's Tauri artifact, and a bundled skill);
6. **No ambiguity when read aloud in Chinese** (the main user base is Chinese-speaking developers);
7. **No collision with a concept already in the product** — for instance `hermes` is already the name of **one of this repo's import sources** (the memories of Nous Research's Hermes agent), so **Hermes is unavailable**;
8. **It must read as both a noun and something you can put in front of a tool**: `<brand>_context` / `<brand>-mcp` / `~/…/<brand>/` all have to roll off the tongue.

### ⑥ The words are still to be picked; the candidate pool (availability already checked)

**Parent brand (engine) candidates**:

| Word | Where the meaning comes from | Upside | Risk |
|---|---|---|---|
| **Lichen** | lichen grows on rock, records the environment, lives extremely long; **lichenometry** dates rock by how much lichen has grown | nearly unoccupied "living thing that records time"; two syllables | the meaning is obscure and needs one explanation; npm already has an `@lichen-ai` scope |
| **Cinder** | the piece that stays red after the fire is out | short, unambiguous, the image of "still alive, no longer burning" | a little cold; no obvious collision found |
| **Attic** | where old things are kept | understood immediately; `github.com/attic` currently returns 404 (available) | implies "old / forgotten", in tension with "memory is alive" |
| **Steward** | the one who keeps another's affairs | semantically exact: the memory layer keeps the project's context for you | leans toward a person; the Chinese transliteration collides with a given name |
| **Colophon** | the note at the end of a book saying how it was made | extremely distinctive, nearly unoccupied; exactly about provenance | three syllables, literary |

**Desktop workbench candidates**: `parent brand + qualifier` (`Bench` / `Desk` / `Board` — `Studio` is taken by Manus and collides with JetBrains Studio) or another standalone word (**Counter** / **Atrium** / **Concourse** / **Agora**); the **zero-controversy fallback** is the README's existing wording, "parent brand + Desktop".

**Agent candidates (Cue-style standalone words)**: **Warden** (the one who keeps watch) / **Torch** (the lamp you carry) / **Corvid** (the crow family, famous for memory among animals) / **Ranger** (the patroller) / **Sherpa** (the guide — **with an ethics note: it is the name of a real people, so commercial use needs care**). **Hermes is ruled out** (constraint ⑦).

### ⑦ No schema change, no change to the MCP tool surface, no contract version bump

This ADR is about **naming and the display layer**. Not one tool name changes → **the contract version does not move** (ADR-0048's `CoreVersion` stays `1.0.0`). The data-directory migration is a **file rename**, not a schema change.

## Costs (accepted)

- **The brand assets are wiped**: the "R" on `build/icon.svg` is no longer the brand's letter, and ADR-0037 decision 1's "recitable construct" has to be re-verified once (does the new letter plus the same hairline rules still hold?). **This is the biggest cost of this ADR**, not the migration.
- **The name and the command line will disagree**: the brand is the new word, `repotory-mcp` keeps the old one. **That is the deliberate cost of decision ③**, but it requires every document to make clear that these are two names for one thing.
- **A fourth data-directory migration**: one more `legacyDirName` generation has to be written (`repotory` → the new name), reusing the existing `migrateLegacyData` failure posture (**a failed rename must not block startup**).
- **Three words have to be settled in one go**: the parent brand, the desktop app and the Agent either change together or not at all; changing half of them is worse than changing none (it adds a fourth way of naming the same thing).
- **History is not rewritten**: the repo will hold both the old and the new name. That is correct, but readers of the history may be confused.
- **Renaming the GitHub slugs** forces old clones to fix their remote once by hand.

## Not decided here (explicitly out of scope)

- **What the three words actually are**: waiting on the user (this ADR carries the candidate pool and the availability findings).
- **Whether the desktop workbench takes "parent brand + qualifier" or a fully standalone word**: settled together with the words.
- **Whether the master art is redrawn**: the letter follows the brand, but the hairline three-domain rules and the "printed card" construct **stay** — this ADR does not touch `build/icon.svg`; the redraw happens with the landing batch.
- **The UI subtraction and hierarchy rework** (metadata drowning content, the permanent right rail, the floating bot covering body text): that is a **separate ADR**, unrelated to naming, and out of scope here.
- **Whether the repo slugs really change**: leaning yes, but the display names may move first and the slugs with the release batch.

## Landing checklist

1. **Word picking**: the user settles the three words from the candidate pool → recorded in this ADR's "Landing record" section.
2. **Main repo display layer**: `wails.json` (`name` / `productName`, **`outputfilename` untouched**), `web/index.html` (title / description / apple-mobile-web-app-title), the product nouns in `web/src/locales/*`, the `name` / `app` stanza in `packaging/homebrew/Casks/*.rb`, `APP_NAME` and the wording in `scripts/install.sh` / `install.ps1`, the headers of `scripts/build-release-assets.sh` / `build.sh`, `.github/ISSUE_TEMPLATE/*`.
3. **Main repo documentation surface**: README (first screen + artifact table + frozen-identifier table), README.zh-CN, the documentation site's home page and sidebar, the current surface of `docs/**` and `docs/en/**` (**historical ADRs / CHANGELOG untouched**).
4. **IDE surfaces**: `displayName` and the command titles in `ide/vscode/package.json`, the `<name>` in `ide/jetbrains`'s `plugin.xml` (**`<id>` untouched** — it is the plugin identifier).
5. **Agent repo**: `productName` in `tauri.conf.json` (**`identifier` untouched**), README (both languages), the naming tables in `docs/product.md` (both languages).
6. **Data-directory migration**: one more `legacyDirName` generation in `internal/platform/platform.go` (`repotory` → the new name), reusing the existing `migrateLegacyData` failure posture (**a failed rename does not block startup**).
7. **Guards**: extend `scripts/check-version-drift.mjs` — **the brand name must have exactly one source of truth on the product surface** (any place holding both the old and the new name must be an explicit allowlist, e.g. the frozen identifiers and the historical documents); `scripts/check-docs-mirror.mjs` keeps guarding the bilingual pair.
8. **Written in the same batch**: CHANGELOG (one entry per language, **the first line is a bold headline**), the `TODO.md` status section, this ADR's "Landing record" section, and the ADR index (one row per language; the next number increments from `0050`).

## Landing record (to be filled in)

To be written once the words are picked and the rename has landed: the three words, the migration tested against a real directory, the negative-case verification, and the list of leftover old names.
