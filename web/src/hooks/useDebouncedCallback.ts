import { useCallback, useEffect, useRef, useState } from 'react'

/**
 * Returns a stable function that invokes `fn` after `delay` ms of silence.
 * The pending timer is cleared on unmount and on each subsequent call.
 */
export function useDebouncedCallback<Args extends unknown[]>(
  fn: (...args: Args) => void,
  delay: number
): ((...args: Args) => void) & { cancel: () => void } {
  const fnRef = useRef(fn)
  useEffect(() => { fnRef.current = fn }, [fn])
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const cancel = useCallback(() => {
    if (timer.current) {
      clearTimeout(timer.current)
      timer.current = undefined
    }
  }, [])

  useEffect(() => {
    return () => { if (timer.current) clearTimeout(timer.current) }
  }, [])

  // The callable is created once and keeps its identity across renders, so
  // a caller can hold on to it (or to its `cancel`) without re-subscribing.
  // The timer and the callback are read from refs inside the closure; the
  // delay is read through a ref too, because a changing delay must not
  // rebuild the callable.
  const delayRef = useRef(delay)
  useEffect(() => { delayRef.current = delay }, [delay])

  const [callable] = useState(() => {
    const debounced = (...args: Args) => {
      if (timer.current) {
        clearTimeout(timer.current)
        timer.current = undefined
      }
      timer.current = setTimeout(() => fnRef.current(...args), delayRef.current)
    }
    debounced.cancel = cancel
    return debounced
  })

  return callable
}
