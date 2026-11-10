import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getScanStatus } from '../api/client'

interface ScanPollingState {
  scanning: boolean
  message: string
  doneMessage: string
}

// How often a healthy backend is polled, and how far the interval grows when
// it stops answering.
const POLL_INTERVAL = 2000
const MAX_BACKOFF = 30000
// Give up after this many consecutive failed polls; a backend that never
// answers must not leave scanning=true forever.
const MAX_FAILURES = 5

/**
 * Polls the backend scan status while a scan runs and invokes `onFinish`
 * once it completes. Replaces the duplicated polling intervals in the
 * Dashboard (initial status check and post-trigger polling).
 */
export function useScanPolling(onFinish: () => void): ScanPollingState & { start: (msg: string) => void } {
  const { t } = useTranslation()
  const [scanning, setScanning] = useState(false)
  const [message, setMessage] = useState('')
  const [doneMessage, setDoneMessage] = useState('')
  const timer = useRef<number | null>(null)
  // Consecutive failed polls, used to back the interval off.
  const failures = useRef(0)
  const onFinishRef = useRef(onFinish)
  // `t` changes identity on every render; keep it in a ref so `poll` stays
  // stable and the mount-check effect below does not re-fire on each render.
  const tRef = useRef(t)
  const alive = useRef(true)
  useEffect(() => { onFinishRef.current = onFinish }, [onFinish])
  useEffect(() => { tRef.current = t }, [t])
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])

  const stop = useCallback(() => {
    if (timer.current) { window.clearTimeout(timer.current); timer.current = null }
  }, [])

  useEffect(() => stop, [stop])

  const poll = useCallback(() => {
    if (timer.current) return
    // Self-scheduling rather than setInterval: the next tick is planned once
    // the previous one settles, so a slow request can no longer pile up
    // overlapping polls, and the interval can back off when the backend is
    // unreachable.
    const tick = async () => {
      let delay = POLL_INTERVAL
      try {
        const s = await getScanStatus()
        if (!alive.current) return
        failures.current = 0
        if (!s.running && !s.backfilling) {
          stop()
          setScanning(false)
          setMessage('')
          setDoneMessage(tRef.current('common.scanDone', { defaultValue: 'Scan complete' }))
          onFinishRef.current()
          return
        }
        setMessage(s.message)
      } catch {
        // A down backend is a normal state here (the local service may simply
        // not be running yet), but polling it every 2 seconds forever burns CPU
        // and fills the log for no benefit. Consecutive failures back off
        // exponentially up to MAX_BACKOFF; one successful poll resets it.
        failures.current += 1
        if (failures.current >= MAX_FAILURES) {
          // No failure exit used to exist: the UI stayed "scanning" forever.
          // Stop and surface the error through the existing message field.
          stop()
          setScanning(false)
          setDoneMessage('')
          setMessage(tRef.current('common.failed', { defaultValue: 'Scan failed' }))
          return
        }
        delay = Math.min(POLL_INTERVAL * 2 ** failures.current, MAX_BACKOFF)
      }
      if (!alive.current) return
      timer.current = window.setTimeout(tick, delay)
    }
    timer.current = window.setTimeout(tick, POLL_INTERVAL)
  }, [stop])

  // Check whether a scan is already running when the consumer mounts.
  useEffect(() => {
    let cancelled = false
    getScanStatus()
      .then(status => {
        if (cancelled) return
        if (status.running || status.backfilling) {
          setScanning(true)
          setMessage(status.message)
          poll()
        }
      })
      .catch(() => { /* backend not ready */ })
    return () => { cancelled = true }
  }, [poll])

  const start = useCallback((msg: string) => {
    setScanning(true)
    setMessage(msg)
    setDoneMessage('')
    // A fresh scan starts from the healthy interval, not from whatever backoff
    // the previous run had reached.
    failures.current = 0
    poll()
  }, [poll])

  return { scanning, message, doneMessage, start }
}
