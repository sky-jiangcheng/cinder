# 0061 Who owns a shared thread's scope — the person, not the workspace

- Status: **Accepted (2026-11-13 — the user picked option A: the thread belongs to the person)**
- Related: ADR-0040 (`LayerProjectCatalog`, the cross-project catalogue layer), ADR-0059 (three-surface behaviour parity gate), TODO 11.1

## Context

Cross-surface shared conversations (ADR-0036) let one thread continue on the desktop, in VS Code and in JetBrains. A deep review found a divergence that was **verified but undecidable at the time** (TODO 11.1):

| | Sends `path` when asking? | What the service does |
|---|---|---|
| JetBrains | **Yes** (`SharedChatSession.transportAsk`, `job.path` captured from `transport.workspacePath()` at creation) | Resolves it to a project → evidence scoped to that project |
| VS Code | **No** | The whole-library catalogue (ADR-0040's `LayerProjectCatalog`) |

The consequence is not an error but **a different answer**: the same thread and the same question are answered from one project in one panel and from the whole library in the other. What the user sees is "the conversation synced but the answers did not" — a credibility problem for the shared-conversation feature itself, not merely a mismatch between two panels.

`path` is an optional parameter of `repotory_ask` and both calls are legal, so **this is not one side's bug**: it is a missing product decision about **who a shared thread's scope belongs to.**

## Decision

**A shared thread belongs to the person, not to the workspace.**

- **Asking carries no scope**: all three surfaces ask without `path` (and without `project_id`), so retrieval goes through ADR-0040's whole-library catalogue layer. The same thread asked from any surface in any workspace yields **the same answer**.
- **`path` keeps a labelling purpose, at creation time only**: creating a thread records the then-current workspace in `thread.path` as a **provenance label** ("this thread was opened in that project"). It does not participate in retrieval.
- **Narrowing the scope is an explicit parameter**: if "ask only within this project" is ever needed, it is a **request-level parameter the user can see** (a visible toggle on the panel), never implicitly decided by which workspace happens to be open.

## Why A rather than "the thread belongs to the workspace"

1. **The selling point of shared conversations is continuing across surfaces and machines.** Binding the scope to a workspace downgrades "shared" to "shared as long as you are in the same repository" — open a different project on a different machine and the same thread changes its answer, which directly contradicts the promise.
2. **Ambient state cannot be a scope.** Which workspace is open is what the user is doing right now, not what they are asking. Letting the former decide the latter makes the answer to one question change as windows are switched — **invisibly and unpredictably**.
3. **An implicit scope turns "the answers differ" into a standing fault.** 11.1 was a problem precisely because it was implicit: the user had no way to explain why the two panels disagreed.

## Consequences

- **A wider answer scope**: an unscoped question sees the whole library. ADR-0040's catalogue layer exists for cross-project questions, and its budget and ordering rules already have their own tests.
- **Less project relevance**: a question about one project's specifics may retrieve items from another. That is A's price, and it is accepted — **when the user wants precision they say so explicitly**, rather than letting the environment guess.
- **`thread.path` narrows in meaning**: from "scope" to "provenance label". It is still written and still readable, but it **no longer affects answers**; any code reading `thread.path` to retrieve evidence is a bug.
- **All three surfaces change together**: KT drops `path` when asking; TS already omits it (stays); Warden must be checked against the same contract if it speaks it. After landing, the fixture rule `scope.path` moves from skipped to settled and each of the three runners gains an assertion — "asking carries no scope" is behaviour, not an implementation detail.

## Adjacent open question (does not block this decision)

**Label conflicts**: when the same thread is created from two workspaces, which one does `thread.path` record? The current implementation keeps the one from creation (first write wins) and does not overwrite later. This is a **labelling** question that does not affect retrieval, so it needs no separate ADR; if "this thread was used from two workspaces" ever needs to be shown, that is a new field and a new decision.
