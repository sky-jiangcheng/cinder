// api/downloadStatus.ts — the app-wide state of "is an update downloading?".
//
// Why a store instead of component state. The download is shown in two
// places at once: a modal progress dialog, and (once the user minimizes it)
// a small card in the corner. Both must show the SAME numbers — a modal
// reading 62% while the corner card reads 41% is worse than either alone.
// The same shape as the update verdict store (api/updateStatus.ts): one
// snapshot, subscribers.
//
// The progress is POLLED, not pushed. The backend downloads in the
// background and exposes the state through a binding; the frontend polls it
// on an interval while a download is in flight. Polling (rather than a
// Wails event) is deliberate: the download is also reachable from the
// headless server's browser UI over the RPC bridge, where a Wails runtime
// event has no listener. Polling works identically in both runtimes.
//
// The store never downloads anything by itself — it exposes start/cancel and
// the state, and the components decide what to show.

import {
  cancelUpdateDownload,
  startUpdateDownload,
  updateDownloadState,
} from './endpoints'
import type { DownloadState } from './types'

export interface DownloadSnapshot {
  /** The backend's progress snapshot. */
  state: DownloadState
  /** Whether the modal progress dialog is showing. */
  modalOpen: boolean
}

const emptyState: DownloadState = {
  phase: 'idle',
  version: '',
  file_name: '',
  downloaded: 0,
  total: 0,
  speed: 0,
  remaining_seconds: -1,
  path: '',
  error: '',
  checksum_verified: false,
  installer_ejected: false,
}

let snapshot: DownloadSnapshot = { state: emptyState, modalOpen: false }
const listeners = new Set<(s: DownloadSnapshot) => void>()

function publish(next: DownloadSnapshot) {
  snapshot = next
  for (const l of listeners) l(snapshot)
}

/** The current snapshot. */
export function getDownloadSnapshot(): DownloadSnapshot {
  return snapshot
}

/** Subscribe to snapshot changes; returns an unsubscribe function. */
export function subscribeDownloadState(cb: (s: DownloadSnapshot) => void): () => void {
  listeners.add(cb)
  cb(snapshot)
  return () => { listeners.delete(cb) }
}

/** True while bytes are moving. */
export function isDownloading(state: DownloadState): boolean {
  return state.phase === 'running' || state.phase === 'verifying'
}

/**
 * Open (or keep open) the modal progress dialog.
 *
 * The modal is the primary surface: a 100 MB download takes long enough
 * that the user needs to see the percentage and the ETA somewhere that
 * cannot scroll away.
 */
export function openDownloadModal() {
  publish({ ...snapshot, modalOpen: true })
}

/**
 * Minimize the modal into the corner card.
 *
 * The download keeps running — this only changes where the progress is
 * shown, so the user can carry on with the app while the bundle arrives.
 */
export function minimizeDownloadModal() {
  publish({ ...snapshot, modalOpen: false })
}

/** Close the modal and hide the corner card (the download may still run). */
export function dismissDownloadUI() {
  publish({ ...snapshot, modalOpen: false })
}

// --- polling ------------------------------------------------------------------

let pollTimer: number | null = null

/** How often the progress is read while a download is in flight. */
const POLL_INTERVAL_MS = 400

function stopPolling() {
  if (pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

function startPolling() {
  if (pollTimer !== null) return
  pollTimer = window.setInterval(() => {
    void refreshDownloadState()
  }, POLL_INTERVAL_MS)
}

async function refreshDownloadState(): Promise<DownloadState> {
  try {
    const state = await updateDownloadState()
    publish({ ...snapshot, state })
    if (!isDownloading(state)) stopPolling()
    return state
  } catch {
    // A failed poll says nothing about the download itself — the backend
    // may be momentarily busy. Keep polling; the next tick usually works.
    return snapshot.state
  }
}

/**
 * Start a download and begin polling its progress.
 *
 * The version travels with the request so the backend can put it in the file
 * name — the bundle the user finds in Downloads says which release it is —
 * and report it in the progress snapshot.
 *
 * Rejects when the backend refuses (a download already in flight, a proxy
 * misconfiguration), so the caller can show the reason.
 */
export async function beginUpdateDownload(url: string, fileName: string, sha256: string, version: string): Promise<void> {
  await startUpdateDownload(url, fileName, sha256, version)
  publish({
    state: { ...emptyState, phase: 'running', file_name: fileName, version },
    modalOpen: true,
  })
  await refreshDownloadState()
  startPolling()
}

/** Cancel the in-flight download. */
export async function abortUpdateDownload(): Promise<void> {
  await cancelUpdateDownload()
  await refreshDownloadState()
  stopPolling()
}

/**
 * Read the current progress once.
 *
 * The settings tab calls this on mount so it shows a download that was
 * started before the tab was opened (or in another window).
 */
export async function syncDownloadState(): Promise<DownloadState> {
  const state = await refreshDownloadState()
  if (isDownloading(state)) startPolling()
  return state
}
