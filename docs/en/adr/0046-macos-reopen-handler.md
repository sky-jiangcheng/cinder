# ADR-0046: The macOS reopen channel — clicking the Dock icon brings the hidden window back

- Status: **Superseded (2026-11-04, [ADR-0047](0047-resident-agent-separate-host.md) decision 5) — this ADR's implementation is deleted from the repo** (`internal/platform/darwinreopen/`) because it **lost its only consumer**
- Date: 2026-11-04
- Related: [ADR-0045](0045-resident-desktop-bot.md) (the resident desktop bot: closing the window does not exit, on macOS the menu bar is the status bar — **its decisions 1 and 2 are superseded by [ADR-0047](0047-resident-agent-separate-host.md)**), [ADR-0047](0047-resident-agent-separate-host.md) (the desktop app goes back to "closing the window quits", a separate project becomes a copilot)

> **Update (2026-11-04, after ADR-0047)**: the disease this ADR treated was **"a hidden Wails
> window cannot be woken"**, and ADR-0047's decision 1 makes the desktop app **stop hiding windows
> altogether** (close = quit), while its decision 5 puts the resident assistant on **Tauri's tray** —
> **Tauri does not travel Wails' `orderOut:` road**; it ships a tray icon and click-to-wake out of the
> box. **So this reopen hook has no consumer left.**
>
> **Disposition**: the implementation (`internal/platform/darwinreopen/`) is **deleted from the repo**.
> **It never entered the git history** — it lived only in this ADR's working tree and was deleted by
> ADR-0047 before it was ever committed, so **there is nothing to take back from the history**
> (`git log -- internal/platform/darwinreopen/` is empty). **What remains is this ADR's analysis.**
> **Why delete rather than keep**: the repo gains Objective-C source for the first time (`.m` + cgo + a
> dependency on Wails' internal class name `AppDelegate`), and keeping **verified-correct code with no
> consumer** has an ongoing cost (the cgo build surface, the fragility that a Wails upgrade
> invalidates) for a problem that no longer exists.
>
> **This ADR's analysis conclusions all still hold** (Wails v2 has no reopen channel, `orderOut:`
> cannot be undone, `@encode(BOOL)` has to be generated per architecture) — **that part is knowledge,
> not code**, and it saves the next person who wants residency in Wails a whole investigation.
>
> See also the first verification in ADR-0047: **Wails v2 has no tray** (`options.App` has no Tray
> field, the `TrayMenu` in `pkg/menu` is a dead type), and the external `systray` needs cgo, which
> makes CI's bare-ubuntu `go build ./...` fail to compile — **that is the hard reason** a resident
> assistant has to change hosts (Tauri v2 ships a built-in `tray` module).

## Background

ADR-0045 made the desktop side resident: quitting the window does not end the process, and on
macOS the menu bar's "Show Repotory" brings it back. **But that was the only way back**, and the
user reported "the app won't open a window" — the investigation confirmed this root cause chain:

1. **A leftover resident instance was holding the single-instance lock** (an older build had no
   lock at all);
2. the next launch therefore quit immediately, and what the user saw was "I double-clicked the
   icon and nothing happened";
3. **on macOS there was no channel at all** that could bring back the window of the instance that
   was still alive.

Point 3 is what this ADR is about.

### State one: macOS has a standard reopen event; Wails v2 does not implement it

macOS has a standard convention: when the user clicks the Dock icon of a running app, the system
dispatches `applicationShouldHandleReopen:hasVisibleWindows:` to the NSApplication's delegate.
That delegate is created by Wails v2 itself — in `internal/frontend/desktop/darwin/Application.m`'s
`Run()`:

```objc
AppDelegate* delegate = [AppDelegate new];
[app setDelegate:(id)delegate];
```

and `internal/frontend/desktop/darwin/AppDelegate.m` **does not contain that method anywhere**.
`AppDelegate.h` declares `@interface AppDelegate : NSResponder <NSApplicationDelegate,
NSTouchBarProvider>`, and `applicationShouldHandleReopen:hasVisibleWindows:` is an **optional**
method of the `NSApplicationDelegate` protocol — not implementing it means no callback at all.

`mac.Options` in `options.App` has **no reopen hook either** (the full set is `TitleBar` /
`Appearance` / `ContentProtection` / `WebviewIsTransparent` / `WindowIsTranslucent` /
`Preferences` / `DisableZoom` / `About` / `OnFileOpen` / `OnUrlOpen` /
`DisableEscapeExitsFullscreen`), and `pkg/runtime` has no matching runtime method.
**Conclusion: Wails v2 has no reopen channel on macOS. That is a capability boundary, not a
configuration problem.**

### State two: why AppKit's default unhide does not save it either

macOS has a default behaviour for "the app is running but has no visible window": unhide the app.
**But Wails does not hide that way.**

In `internal/frontend/desktop/darwin/WailsContext.m`:

```objc
- (void) Hide { [self.mainWindow orderOut:nil]; }
- (void) Show { [self.mainWindow makeKeyAndOrderFront:nil];
                [NSApp activateIgnoringOtherApps:YES]; }
```

`orderOut:` takes the window out of the window list — **the window is still there, but it is no
longer in the on-screen window list**. AppKit's default reopen behaviour restores "the app's
windows", not "a window that was ordered out". So the result of clicking the Dock icon is:
**the app got activated (the menu bar came back), but nothing appeared on screen.**
That matches the user's "I clicked and nothing happened" exactly.

### State three: `showWindow()` failed silently

The three functions in `main.go` that bring the window back all looked like this:

```go
func showWindow() {
	ctx := runtimeContext()
	if ctx == nil {
		return          // ← silent return, not a single log line
	}
	wailsruntime.WindowShow(ctx)
}
```

`runtimeContext()` returns nil before `OnStartup`. **What the user sees is "nothing happened",
and the log says nothing at all** — there was nowhere to start from during the investigation.
This silent failure carries one more hard risk: `getFrontend(ctx)` in `pkg/runtime` does
**`log.Fatalf` and exits the process** when ctx is nil, so that `if ctx == nil` branch is not
merely "doing nothing" — it is **shielding a call that would crash the process**. It shields it,
but silently.

## Decision

**① The reopen hook is installed through an Objective-C runtime injection; ADR-0045's hiding
mechanism stays as it is.**

The delegate **instance** belongs to Wails (it news it up and sets it as the delegate), so it
cannot be swapped. But the delegate's **class** can be touched — the Objective-C runtime's
`class_addMethod` can append the missing selector to the class, and that implementation can call
back into Go.

So a new package `internal/platform/darwinreopen/`:

| File | Job |
|---|---|
| `reopen.go` | the common layer: handler storage (`sync.RWMutex`) + the `trigger()` bridge + three sentinel errors |
| `reopen_darwin.go` / `reopen_darwin.m` | the darwin+cgo implementation: the runtime injection + the C callback |
| `reopen_other.go` | the stub for non-darwin or cgo-less builds (returns nil, does nothing) |

`main.go` calls `darwinreopen.Install(showWindow)` **before** `wails.Run` — Objective-C classes
are registered when the binary is loaded, so arming it early is steadier than arming it late (it
does not depend on any delegate instance already existing).

**② The hook only brings the window back; it does not open the bot panel.**
Clicking the Dock icon means "call the app back", not "start a conversation on the user's behalf".
The bot panel is the app's home page, **but that does not mean every wake-up should pop the
panel** — the menu's "Show Repotory" and "Ask Repotory…" already do these two things separately.
Reopen is the former.

**③ A hook that cannot be armed is logged, not fatal.**
`Install` returns an error (`ErrNoDelegate` / `ErrAlreadyInstalled` / `ErrInstallFailed`) and
`main.go` logs one line and keeps going. **A resident app's way back should have a primary and a
backup**: when the primary (the Dock click) cannot be armed, the menu bar path is still there.
After arming, if `class_getInstanceMethod` finds the selector already present (a future Wails
implementing it itself), `Install` returns `ErrAlreadyInstalled` and **does not override it** —
**whoever owns the behaviour owns it**, this repo does not grab it.

**④ All three `ctx == nil` branches get a log line.**
`showWindow` / `askBot` / `quitApp` each get a `log.Printf` saying "the app context is not ready
yet, the window was not brought back". **Silent failure was the biggest time sink in this
investigation**, and there must not be a second one.

**⑤ Not one decision of ADR-0045 changes.**
"On macOS use `WindowHide` rather than `HideWindowOnClose`" stays verbatim —
`HideWindowOnClose` goes to `[NSApp hide:nil]` (hiding the whole app, which hands the menu bar
to the front app), which directly contradicts "the menu bar stays resident"; **that is not this
ADR's account to settle.** This ADR **adds** a path to the one ADR-0045 left open, it does not
change it.

**⑥ No new dependency, no new MCP tool, no schema change.**
The hook is process chrome; it has nothing to do with knowledge capabilities.

## Consequences

- **Positive**:
  - **Clicking the Dock icon brings the window back** — the most natural gesture on macOS, and
    it previously did nothing at all.
  - A failed wake-up is no longer silent: the log shows either "the context is not ready" or
    "the hook was not armed".
  - There are now two ways back: the Dock click (primary) and the menu bar (backup).
- **Cost (accepted)**:
  - **The repo now contains Objective-C source** (`reopen_darwin.m`). `.m` files honour
    `//go:build`, and this ADR uses `darwin && cgo` to keep the blast radius at "the macOS
    desktop app" — `go build ./...` takes the stub on ubuntu / windows /
    `CGO_ENABLED=0`, so **none of CI's three gates are affected**.
  - **It depends on one internal Wails class name** (`AppDelegate`). A rename would stop the
    hook from arming — which is why failing to arm must **be logged** rather than silent, so the
    next person can see "the hook was not armed" in the log straight away.
  - **The hook exists on macOS only.** The Windows / Linux ways back are unchanged (taskbar icon
    + second instance), and this ADR does not touch them.
  - **The selector's type encoding is assembled at runtime** (`@encode(BOOL)`), because BOOL
    encodes as `c` on x86_64 and as `B` on arm64 — hardcoding one breaks the other architecture.
- **What this does not solve**:
  - **This is not a general "the window won't start" repair.** The other root cause from the
    previous investigation was **a leftover resident instance holding the single-instance lock**,
    which is solved by clearing the process and the lock file; the hook has nothing to do with
    it.
  - **When the hook cannot be armed, the menu bar is the only way back.** That is an acceptable
    degradation, but **it has to be visible** — which is what decision ③'s logging is for.

## Implementation checklist (this ADR is superseded; the table below records **the plan at the time**, not the current state)

> ⚠️ **None of the three rows below ever entered this repository.** The hook was written and verified
> in ADR-0046's working tree, but it was deleted by ADR-0047 before it was ever committed (decision ⑤)
> — `git log -- internal/platform/darwinreopen/` is empty. **This table is kept to record "what was
> planned back then", not "what exists now".**

| Surface | File (**never committed**) |
|---|---|
| The hook itself | `internal/platform/darwinreopen/` (`reopen.go` / `reopen_darwin.go` / `reopen_darwin.m` / `reopen_other.go`) |
| Wiring and logging | `main.go` (the `darwinreopen.Install(showWindow)` before `wails.Run`; log lines in the `ctx == nil` branches of `showWindow` / `askBot` / `quitApp`) |
| Unit test | `internal/platform/darwinreopen/reopen_test.go` (the handler bridge; the platform hook itself needs a real NSApplication and is covered by the on-machine verification) |
