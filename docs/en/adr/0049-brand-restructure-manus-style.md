# ADR-0049: Rebuild the brand the Manus way — a new parent brand, an independent name per surface, the identifier layer frozen

Status: **Accepted (2026-11-05, the user picked option (c) "full Manus-style restructure" among the four routes offered for "how do we redefine the brand and the UI"; the same day the three words were settled: parent brand **Cinder** / desktop **Cinder Desktop** / Agent **Warden** — the landing process is recorded under "Landing record" at the end)**
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

### ⑥ The words (candidate pool and availability already checked)

**Settled (see "Landing record" at the end): parent brand Cinder / desktop Cinder Desktop / Agent Warden.**

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

## Landing record (added 2026-11-05)

### The three words

| Slot | Word | Where it comes from |
|---|---|---|
| Parent brand (engine / memory layer) | **Cinder** | from the candidate pool — "the bit still glowing after the fire goes out": persistent, but no longer burning |
| Desktop workbench | **Cinder Desktop** | the wording the README already used ("parent brand + qualifier"); no new word invented |
| Agent (the copilot in the separate repository) | **Warden** | from the candidate pool — "the night watchman": an independent product, no longer an "X Agent" |

The three words were **settled in one go** (decision ②'s cost: changing half of them is worse than changing none). The parent brand and the Agent deliberately come from different semantics — **the engine and the assistant are not the same role**, which is exactly what "the name cannot be older than the decision" means.

### Two deviations from the landing checklist (both toward "frozen")

Checklist item 6 and decision ③ **contradict each other**: the checklist asks for one more `legacyDirName` generation (the data directory `repotory` → the new name), while decision ③ states that "the tool prefix, the binary name, **the data directory** and the MCP server name do not move by a single character". **Decision ③ wins**:

1. **The data directory is not migrated** (the fourth migration is cancelled): `dirName = "repotory"` and `legacyDirName = "reponest"` in `internal/platform/platform.go` are **untouched**, and only an explanatory comment was added (why the brand changed but the directory name did not). **Cost**: the user's data directory is still called `repotory`, and the command line is still `repotory-mcp` — that is the deliberate cost written into decision ③. **Gain**: no fourth migration, no skipped-generation user (a data directory left at `reponest` or older) losing their directory, and **no room left for "one more migration" afterwards**.
2. **The DB sentinel does not move and the schema does not bump**: `repotory://memory/default` in the database is **data**, not schema, but it is just as much "a string on somebody else's machine" — treated as part of the identifier layer, untouched, **the schema version stays v28**. Changing it and bumping the schema was considered during the landing discussion and **finally settled as frozen**; it is written down here so the next person does not assume it was an oversight.

### The master mark and the icons

- The letter in `build/icon.svg` went **R → C**: the glyph is the C of IBM Plex Sans Bold (© IBM Corp., SIL OFL 1.1), outlined and scaled to a 320-unit cap height, **centred on its visual mass rather than on its bounding box** — the C's aperture opens to the right, so the ink sits 9 units right of dead centre (the R it replaced sat under half that rule: 6 units). **The hairline frame (9 units, inset 26) and the paper field `#F5F0E4` did not move by a character** — what ADR-0037's decision 1 protects is that rule, not that letter.
- Every derivative was regenerated: the brand domain (favicons / the stores / the brand mark inside the JetBrains tool window), the OS app domain (appicon / the ladder / ico), and the glyph domain (the VS Code activity bar 24px, the JetBrains sidebar 16px plus `_dark`, the macOS tray 22px in one colour on transparency).
- **`scripts/generate-icons.mjs` gained `--agent-repo <path>`**: one command now derives the whole Tauri icon set for the separate repository (15 PNGs + `icon.ico` + `icon.icns` + `tray-22.png`). **That was a real gap**: the agent repository's AGENTS.md forbids hand-editing those PNGs, yet the main repository's generator had **no path that emitted them** — that set was hand-drawn, and not from the same master. Now it is.
- The generated `icon.icns` was round-tripped through `iconutil -c iconset` back into 10 slots (macOS reads it natively; one trap was hit on the way: the ICNS chunk length field **includes the 8-byte chunk header**, and writing it without the header yields a file where only one slot can be extracted).

### Display layer (main repository)

`name` / `productName` in `wails.json` (`outputfilename` untouched), `web/index.html`, the locales, the Homebrew cask's `name`, the install and build scripts, ISSUE_TEMPLATE, the READMEs (both languages), the documentation site, the VS Code extension's `displayName` and command titles, and the JetBrains `plugin.xml`'s `<name>` (`<id>` untouched).

**The DMG window canvas is a display surface too**: `build/dmg-background.svg` used to carry the old product name plus a subtitle that had been wrong for three generations ("Git 代码提交统计面板" — the positioning of three generations ago); it now carries the new desktop name and "AI 编码 Agent 的本地记忆层".

**`build/dmg-readme.txt` was deleted**: an unreferenced dead file holding an even older brand name.

### Display layer (separate repository)

`productName` and the window title in `tauri.conf.json`, the READMEs in both languages, `AGENTS.md`, `docs/product.md` in both languages, and the UI copy — **`identifier` untouched**.

Real defects fixed along the way (not style issues — they came out of the rename):

- **9 missing spaces**: the bulk replacement ate the space in "Latin word + space + Han character" (8 in the separate repository, 1 in the main one) — "Cinder Desktop是两个产品" and the like.
- **1 misaligned ASCII diagram**: in `docs/roadmap.md`, `Repotory` → `Cinder` is 2 columns shorter, so the arrow column shifted → 2 spaces added, the columns restored.
- **5 broken anchors**: **changing a heading changes the anchor**. After the rename, 4 links pointed at nothing (plus one that was missing a 的 — "ADR-0015 人类闸门" — an older hand-typing slip). All fixed.
- **1 self-contradicting statement**: after the rename, 4 places said "Cinder is the **workbench**", which contradicts the README's own role table (where Cinder is the memory layer) → changed back to "Cinder Desktop is the workbench".

### Guards (one per repository)

| | Main repository | Separate repository |
|---|---|---|
| Source of truth | `build/brand.json` | `brand.json` |
| Script | `scripts/check-brand.mjs` | `scripts/check-brand.mjs` |
| Entry point | CI `brand` job | CI `brand` job (the fourth gate) + `npm run check:brand` |
| What it checks | the product name at 8 sites equals brand.json; **the marketplace name at 2 sites matches exactly**; the 12 frozen identifiers are still there verbatim; the current surfaces carry no old brand word (historical surfaces are allowed by prefix); the app's `BrandMark` comes from the master mark (path `d`, group transform, hairline width, inset, both colours) | the product name at 13 sites equals brand.json (including `productName`, the window title, `<title>`, the first-screen name); the 6 frozen identifiers are still there; the current surfaces carry no old brand word; **the document anchors still point at a heading** |

**The negative cases were verified** (a gate that can only go green is not a gate): in the main repository, five — the product name reverted to the old name, **the marketplace name changed back to the taken one**, the data directory renamed, the README leaking the old name, the `BrandMark` path off by one — each goes red; in the separate repository, five — `productName` reverted, `identifier` "tidied up", an old brand word on a current surface, an old brand word inside an anchor, the desktop name no longer derived from the parent brand — each goes red. Both repositories are green again after restoring.

**Each repository has its own `brand.json`**: CI runs on one repository at a time and cannot see the other, so **a word change has to land in both in the same batch**. That is the one structural weakness of this source of truth; it is noted here.

### Addendum: the marketplace name was taken (2026-11-05, the day v2.31.0 shipped)

In the v2.31.0 release pipeline, **exactly one cell went red — the VS Code Marketplace publish**:

```
##[error]This extension display name is taken.
```

Not the build, not the signature, not a version conflict — **the VS Code Marketplace requires `displayName` to be unique across the whole marketplace**, and the display name "Cinder" was **already held by somebody else's extension** (`muowl.cinder-warm-theme`). The JetBrains Marketplace has no such rule, so the JetBrains plugin published successfully (id 35396).

**That cell cannot be published twice**: one extension id at one version number may only correspond to one artifact, so "publish 2.31.0 again under a different name" is not available — it would leave two different artifacts claiming to be 2.31.0, which breaks the "one version, one artifact set" release contract (ADR-0017) and leaves the published `SHA256SUMS` pointing at the wrong files.

**Therefore**: the IDE plugins' **marketplace name** is **Cinder Memory** (the parent brand plus a qualifier), the same on both IDEs. The **artifact ids stay exactly as they are** (`sky-jiangcheng.repotory-vscode` / `dev.repotory.jetbrains`) — the identifier layer stays frozen; on the marketplace this is the same listing under a different name, not a new one. **The command titles inside the editor stay "Cinder: …"** — that is the parent brand word, and the interface has to read as the same family; the qualified name is the **marketplace listing name** (the title on the Marketplace, the one line in the extension list).

**The guard was strengthened**: the brand gate used to check that a file *contains* the parent brand word — and "Cinder" contains it too, so the old gate was blind to exactly this failure and stayed green the whole way. `build/brand.json` now carries one more key, `idePlugin`, and the gate compares the two IDE plugins' marketplace names **exactly** (no longer "contains"), and requires them to start with the parent brand plus a space. The negative case was verified: putting both names back to "Cinder" turns the gate red immediately.

**Both repositories in the same batch**: the `idePlugin` key was added to the separate repository's `brand.json` as well (that copy ships nothing; it only keeps the two sources of truth from diverging in shape).

### Leftover old names

- **Kept on purpose (identifier layer)**: the binary names, the data directory, the database file name, the MCP server name, the tool prefix, the Go module, the VS Code / JetBrains plugin ids, the npm package name, the cask token, the skill name, the Windows install directory, the GitHub slugs.
- **Kept by convention (historical surfaces)**: the historical ADRs / CHANGELOG / review records / the historical entries in `TODO.md`.
- **Fixed**: the DMG window canvas, and the separate repository's first screen and menu hint ("点菜单栏的 R 唤回窗口" → "C").
- **Deleted**: `build/dmg-readme.txt`.
- **Outstanding**: the 9 screenshots under `outputs/screenshots/` in the separate repository were taken **before the rename** (the first screen shows the old name) — a `README.md` was added saying they are historical evidence, and they were **not retaken** (retaking means bringing up the Tauri window with a real MCP server and a real provider).
- **Unrelated to the rename, broken for longer**: 5 internal document links in the main repository (`docs/data-management.md` and others); not fixed in this batch.

### Left alone (needs separate approval)

- **The GitHub repository slugs**: `sky-jiangcheng/repotory` and `sky-jiangcheng/repotory-agent` **stay as they are**. Renaming a slug makes every old clone change its remote by hand, which is shared state and needs its own decision; **this has to go into the release notes**.
  - **→ separately approved and executed in "Addendum two" (same day, 2026-11-05)**: the slugs finally became `sky-jiangcheng/cinder` and `sky-jiangcheng/warden`. The sentence above is that day's judgement and **is kept as it is** — decision ④ says a slug may change but needs its own approval, and approving versus not approving are two separate decisions; erasing the first would hide why the second exists at all.

### Addendum two: the repository slugs really did change (2026-11-05, same day)

The section above says the slugs stay as they are and need separate approval — **that approval was given and acted on the same day**.

**What changed**: `sky-jiangcheng/repotory` → **`sky-jiangcheng/cinder`**; the separate repository `sky-jiangcheng/repotory-agent` → **`sky-jiangcheng/warden`**. Both repositories' **description and homepage were fixed at the same time** — they were stale copy from years back (the main repository said "Git commit statistics tool", the separate one said "RepoNest"), and **a renamed repository whose blurb still carries the previous project's name is worse than one that was never renamed**.

**The scope was the repository and outward-facing name layer only**: the GitHub slugs, the repository descriptions and homepages, every in-repo link pointing at those two repositories, the documentation and the release notes. **Not one character of the identifier layer moved** — all twelve frozen items of decision ③ stand. That is not a contradiction: decision ④ puts the slug on the "may change" side, while decision ③ governs the `repotory_*` literals that **other people's machines recognise**. A slug is recognised by other people too, but **renaming it breaks nothing already installed** — unlike renaming an MCP tool, which breaks every user's `claude mcp add` on their machine.

**Three things that have to go into the release notes**:

1. **Old clones must fix their remote once by hand**. GitHub answers the old repository URL with a **301 redirect** after a rename, so `git clone` / `git fetch` against the old URL **still work**; but a **repository that was already cloned** still points its remote at the old address, so `git pull` keeps going through the redirect — **it works, with one extra hop every time**. Fix it once: `git remote set-url origin https://github.com/sky-jiangcheng/cinder.git`.
2. **GitHub Pages does not follow the redirect**. After the rename, the old documentation-site path `sky-jiangcheng.github.io/repotory/` **returns 404** (measured), and the new path `sky-jiangcheng.github.io/cinder/` **returns 200** (measured). **That is GitHub's behaviour, not a bug in this repository** — a rename redirects **repository addresses** only, and the **Pages site path follows the repository name and does not inherit that redirect**. The documentation-site links therefore have to move **in the same batch**, or a batch of old links silently turns into 404s — **which is exactly why the Pages path and the slug went into one commit**.
3. **The 301 on the old URL is not a permanent guarantee**. GitHub does not promise to keep a rename redirect forever; **links pointing at the old repository (READMEs, issues, third-party documentation) will eventually break**.

**A structural problem the rename forced into the open**: once the repository name changed, the assumption that **one repository equals one project** started to get in the way — **the next project that wants to live in the same pair of tap and bucket would mean editing the hardcoded path in every script**. So the same batch added a **namespace layer** to the packaging manifests (`packaging/<project>/<target>/…`), and the tap and bucket are now **one pair of repositories shared by every project**: a new project only has to drop in a directory, and not one script has to change. See the CHANGELOG entry "the packaging manifests gain a namespace layer" and `packaging/README.md`.

**Guard**: the separate repository's `check-brand.mjs` used to carry the upstream main repository's address as a **frozen item** — it would have gone red the moment the main repository was renamed, because **it froze somebody else's repository name**. That entry is now **derived from the parent brand word** instead: `https://github.com/sky-jiangcheng/<parent brand, lowercased>`, so the address follows the brand, and **it is no longer a hardcoded literal**. Negative case verified: change the parent brand to `Cinderx` and the gate goes red three times over.

**Both repositories gained one more check in the same batch**: each side's `check-brand.mjs` now also checks that **a renamed repository's address does not survive on a current surface** — current surfaces (historical ones stay exempt by prefix) must not carry an old repository address **qualified by the organisation prefix**. **Why "organisation + stem"**: the only stems this project has ever published under are `repo-nest` and `repotory` (the latter is also the stem of the separate repository's `repotory-agent`), and everything after the stem belongs to the same dead address, so the stem is what gets hunted; and the **organisation prefix is the line between an address and a mention of the old name in prose** — without the prefix, `repotory` is the identifier layer (the literals decision ③ freezes), which is not this check's business. **It reads the raw line, not the comment-stripped one**: the comment stripper cuts at `//`, and every `https://` contains a `//`, so a stripped line would have had the address cut off before the scan — **a bug that would leave the gate looking green**. Negative cases verified: an old repository link in the README, the old documentation-site path and a releases URL in source each turn the gate red; green again once they point at the new addresses.

**Not done**: renaming the repository slug **moves no data** (the user's data directory, database file name and tool prefix all live in the identifier layer and did not move); **no redirect service was stood up for the old name** — GitHub's 301 already covers clone and fetch, the two most common uses, while the Pages path **never inherited the redirect in the first place**; covering that would mean running another service, whose cost does not match its benefit, so the new path went into the release notes and the documentation-site navigation instead.

### Verification

- Main repository: `node scripts/check-brand.mjs` green; `node scripts/generate-icons.mjs --check --tracked-only` green (the master mark and every committed derivative agree).
- Separate repository: `npm run check:brand` green; `npm run check:docs` green; `npm run build` (`tsc` + `vite build`) green; `cargo fmt --check` and `cargo clippy --all-targets --features test-fixtures -- -D warnings` green; `cargo test --all-targets --features test-fixtures` **80 passed / 0 failed**.

### Still open (left as it was)

- **UI subtraction and hierarchy rework**: metadata drowning the content, the permanent 288px right rail, the floating bot covering the body text — diagnosed, and an **ADR of its own**, unrelated to naming.
- **Whether the master mark gets redrawn**: this round only changed the letter; the hairline three-domain rules and the "printed card" construction stay.

