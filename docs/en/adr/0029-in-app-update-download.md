# ADR-0029: In-app update download — a modal progress dialog plus a corner card, never a self-replacement

Status: **Accepted (implemented and browser-verified end to end, 2026-10-25)** — `internal/service/update.go` provides the advisory-only check, the exact-shape asset filter and the SHA256 verification; the frontend `UpdateDownloadUI` provides the modal / corner-card pair; the navigation badge and the settings card share one check. The real GitHub API path verified clean ("you are on the latest version", v2.13.1), and the download states were reproduced by interception and inspected screenshot by screenshot — see `outputs/e2e-2026-10-25/`
Date: 2026-10-25
Related: [ADR-0006](0006-scope-freeze.md) (scope freeze — this is the desktop app maintaining itself, not a product-boundary expansion), [ADR-0025](0025-backup-recovery.md) (the local-first, nothing-leaves-the-machine posture extends here into "never replaces itself"); no schema change in this ADR
The owner's original request (2026-10-25): "follow Qwen Office's approach to the update download details" — the reference is Qwen Office's update download UX: **a progress modal + minimize to a corner card + a completion dialog + release notes + the current version**

## Background

Until now the desktop app only had "Settings → Check for updates", which jumped straight to the GitHub release page and left the download to the user. That path has three problems:

1. **A 100 MB bundle is waited on in the browser.** The release page offers a link; the progress, the speed and the ETA all live in a browser tab, while the thing the user most wants to do at that moment is keep working in the app.
2. **Neither the current version nor the release notes are shown.** "Is there a newer version?" is a riddle — the user has to compare tags themselves.
3. **The app and the bundle are disconnected.** The user has to hand the downloaded file to the system installer, and nobody is responsible for the integrity step in between.

Qwen Office's handling is worth copying: **the download happens inside the app, the progress has a modal, the modal can be minimized into a small card in the lower-left corner so the user can carry on, a finished download gets an explicit completion dialog (with the verification result and the install entry point), and the current version plus the release notes are shown throughout.**

One thing should not be copied though: **the app updating itself**. Letting a program replace itself hands the execution rights of a network transfer to the running code.

## Decision

**1. Advisory only: detect, never auto-download, auto-install or auto-replace the binary.** A detected new version is only announced; the download is triggered explicitly by the user; when it finishes the file is handed to the **system installer**, and the user decides when to install. The reason is written at the top of `internal/service/update.go`: trust surface.

**2. Asset filtering must match the exact shape.** `appAssetPattern = ^repotory-(darwin|windows|linux)-(amd64|arm64)\.(dmg|zip|tar\.gz|AppImage|deb|msi)$`. The same release also carries `repotory-mcp-*` / `repotory-server-*` / the VSIX / the JetBrains plugin zip — all of them contain "repotory" and a platform token, so a **looser match would offer the user an MCP server archive as "the new version of the app"**. When the release ships no bundle for the running platform (CI does not build linux/arm64), the UI falls back to the release page.

**3. One single source of truth: a module-level store.** `web/src/api/updateStatus.ts` holds the verdict; the navigation badge and the settings card share the same check. Two independent checks would hit GitHub's API twice per launch and could contradict each other — a badge saying "up to date" next to a card saying "2.13.0 available" is worse than either alone. A failure keeps the last successful verdict: one network hiccup should not wipe known information off the screen.

**4. Two progress surfaces, one snapshot.** The modal (the primary view — 100 MB deserves a progress bar and an ETA that cannot scroll away) and the lower-left corner card (the minimized form, so the user can carry on) read the same `api/downloadStatus.ts` snapshot, so a "62% in the modal against 41% in the card" state is impossible. The card is mounted at app level (`App.tsx`), not inside the settings tab — the download has to survive navigation.

**5. Polling, not Wails events.** The backend downloads in the background and exposes the state through a binding; the frontend polls every 400 ms. The reason: the headless server's browser UI goes over the RPC bridge, where there is no Wails runtime event listener; polling behaves identically in both runtimes.

**6. "Download finished" means the SHA256 verification passed.** Only then is it reported as verified; otherwise the UI says plainly that it is unverified. On completion the user gets two buttons: later (dismiss) and open installer (hand it to the system).

**7. `OpenBrowserURL` allows https only.** Opening the download link in the system browser is the one path that leaves the app, so the protocol is locked down.

**8. The startup check has two gates and fails silently.** The user's switch (`update_check_enabled`) plus the desktop runtime (a browser tab pointed at a headless server has no use for self-maintenance, but the manual button in the settings tab still works there). Failures are silent — a machine that cannot reach GitHub should not see an error banner across the whole app on every launch; the settings tab re-runs the check on demand and shows the reason there.

**9. Runtime split.** `openBrowserURL` goes through the Wails binding on the desktop and `window.open` in browser mode — the headless server's UI is also a browser tab, where the Wails binding has no context and must fail.

## Options matrix (how to do the download)

| Option | Value | Main risk |
|---|---|---|
| **This decision: download in-app, verify, hand to the system installer** | the user never has to leave the app; integrity is accounted for; progress is visible | the app has to implement a downloader and a verification step; the system installer still has to finish the job |
| Download in-app and replace the binary automatically | least effort for the user | **trust surface**: running code is replaced by the result of a network transfer, and a failure can leave a half-broken app on disk |
| Only link to the release page (today's behaviour) | cheapest to implement | progress and verification are handed to the browser; nothing like the Qwen Office experience |
| Go through a system package manager (brew / scoop) | consistent with the distribution channels | bypasses the SHA256SUMS self-verification; and the user may not have the relevant package manager installed |

## Consequences

- Positive: the update experience matches Qwen Office's (progress modal / corner card / completion dialog / release notes / current version); the download happens inside the app and is verified; the app never replaces itself.
- Negative (stated plainly): an in-app downloader has to handle interruptions, sizes, verification failures and cancellation — a fair amount of code; the last step still depends on an external program, so it is not fully closed; the startup check is one outbound request, and even a silent failure is one network attempt per launch.

## Implementation corrections (facts that differ from the design)

1. **The release notes are rendered without `utils/markdown.ts`.** That module pulls KaTeX and highlight.js into the same lazy chunk; the release notes are just a Markdown blob, and using it would drag two heavy dependencies into the update path. `marked` plus `DOMPurify` are used directly instead.
2. **The real release asset names are the only reliable source for the filter**: `repotory-mcp-*` / `repotory-server-*` / `repotory-vscode-*.vsix` / `repotory-jetbrains-*.zip` / `SHA256SUMS` coexist with the four app bundles. Running the filter against a real GitHub API response after a release is the cheapest way to be sure.
3. **The "a newer version exists" state is hard to reproduce naturally in a browser**: when the local build is the latest, the check always answers "you are on the latest version". The inspection intercepts the `CheckUpdate` response of `POST /api/rpc` to construct the state — the interception only affects frontend observation and changes no product code.

## Amendment (2026-11-03): the bundle lands in the system's download folder, and the path and version are visible

One real-world use of this flow exposed three things a user could not judge: where the file went after the download, which version was downloaded (the file name was always `repotory-darwin-arm64.dmg`, identical for every release), and why the Finder showed two Repotory icons after the install (one is the installed app, the other is the still-mounted DMG volume). None of the three is a functional defect — they are **information the app had but never showed**.

**10. The download directory is now the system's download folder.** It used to be the app's own config directory (`~/Library/Application Support/repotory/updates` on macOS). A config directory is the right home for *state*, and the wrong home for a file the user has to find and drag into place by hand — a bundle hidden under "Library/Application Support" is a bundle the user never finds, which is where the first question ("which folder did it download to?") came from. The bundle now lands in the system's download folder (`Downloads` under the user's home on macOS and Windows; `XDG_DOWNLOAD_DIR` when Linux exports one) — the same place a browser puts it, so the user recognises it and knows where to look.

The cost: the download folder belongs to the user, not to the app. So **state is stored elsewhere** — `update-download.json` goes back into the app's data directory, and the app leaves no file of its own in Downloads.

**11. The file name carries the version.** `repotory-darwin-arm64.dmg` → `repotory-2.25.0-darwin-arm64.dmg`. The name was byte-for-byte the same for every release, so "is this bundle the version I am on?" could only be answered by guessing; with the version in the name, the answer is in the name. The progress snapshot now carries the version too (`begin()` used to pass an empty string, so the dialog's "version x.y.z" line had never once been shown).

**12. The download state is persisted.** The progress used to live in memory only: restart the app and "open installer" answered "there is no finished download to open", while the file was still sitting in Downloads. The terminal state is now written to `update-download.json` in the app's data directory and restored at startup — **but only when the file is still there**: a state pointing at a bundle that has been moved or deleted is dropped rather than restored, because an "open installer" button that fails on a missing path is worse than no button at all.

**13. The path is visible, and so are the actions.**
- The completion dialog shows the **full path** — that is exactly what the user was asking for;
- A new "show in file manager" action (macOS `open -R`, Windows `explorer /select,`, Linux `xdg-open`);
- A new "installation" block in the settings tab: one line for the install path, one for the download folder — "where did it install?" now has an answer;
- A new `GetInstallInfo` binding. `install_path` is reported by the running binary itself (it walks up from the executable to the `.app` bundle) rather than hardcoded.

**14. The installer volume gets ejected.** A mounted DMG on macOS stays in `/Volumes` until it is ejected, and closing its window is not ejecting it — so the Finder sidebar kept a second Repotory, which is the "two icons after the install". There are now two ways out: the user presses "eject installer volume" (only a volume holding **this app's own bundle** is ejected; a disk image the user mounted themselves is never touched), and the app ejects it automatically once it detects the install. The detection reads `CFBundleShortVersionString` from the installed bundle's `Info.plist` — the version on disk, not the running process: after the bundle is replaced, the running process still holds the old code in memory, so the version on disk is the honest signal.

**15. The state file's path is settled when the download starts (fixing a data race).** The state is written by the download goroutine's terminal hook, and the path used to be **looked up on that goroutine**. A test points the path at a temp directory so it never touches the real user's data — which means "the test's cleanup restores the override" collides with "the download goroutine reads the override", and `go test -race` reports a DATA RACE. Worse: if the goroutine read the override *after* it was restored, the state would be written into the **real user's data directory**. The path is now resolved once at download start (on the caller's goroutine) and carried into the goroutine, exactly like the download directory — and the terminal hook is installed at download start too, so the hook carries the path with it. The override variables' reads and writes are now ordered by a single lock.

### What the amendment costs

- Positive: the download location, the version and the install location are all visible and checkable; a downloaded bundle no longer "disappears" across a restart; the mounted installer volume leaves by itself after the install.
- The cost: one more state file to write, one version-probe loop over the install directory (every 3 seconds, for at most 10 minutes), one `hdiutil detach`; and the download folder is now shared with the browser — the app only "puts a file there" and can no longer assume it owns it.
- **Regression**: 12 new backend tests (the download directory lands in Downloads / the file name carries the version / the progress snapshot carries the version / the state survives a restart / a state whose bundle is gone is dropped / reveal requires a finished download / only our own bundle is recognised / no volume means no eject / the install info carries the path / the Info.plist version is read / the digest verdict also survives a restart / **a finished download writes its state on its own**); 8 new frontend tests (the completion dialog shows the path / the completion dialog shows the version / reveal in the file manager / macOS offers eject / non-macOS does not / nothing to eject is reported / a successful eject is confirmed / the settings page shows the install path and the download folder). `go test -race ./internal/service/ ./internal/app/` is green across four consecutive runs.
