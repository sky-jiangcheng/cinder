import s from './TabStrip.module.css'

// The one tab implementation (ADR-0039 decision 5).
//
// The project page, the settings page and the quick-capture FAB already had
// tabs; the review, knowledge and dashboard pages did not, and each rolled its
// own "small heading + stack of sections" instead. Two ways to switch within
// one product is one too many — so the new tabs use THIS component, and the
// styling is the design system's existing global `.tab-btn` / `.tab-active`
// rather than a second visual language.
//
// What it provides, all of it non-negotiable for a tab:
//   - role="tablist" / role="tab" / aria-selected / aria-controls, so the
//     strip is a real tablist and the panel it controls is announced;
//   - roving tabindex plus Arrow / Home / End, so the strip is reachable and
//     operable from the keyboard alone;
//   - an optional count badge — the number is what tells a user whether a tab
//     is worth clicking, and tabbing hides everything outside the active tab.
//     That is why the badge is part of the component rather than something
//     each page re-invents (and forgets).

export interface TabItem {
  key: string
  label: string
  /**
   * The count badge. `null` / `undefined` means no badge at all; `0` is a
   * real number and IS shown — "Pending 0" is information ("the queue is
   * empty"), while a missing badge says nothing.
   */
  count?: number | null
}

interface TabStripProps {
  items: TabItem[]
  active: string
  onChange: (key: string) => void
  /** The accessible name of the tablist. */
  ariaLabel: string
  /**
   * Prefix for the generated ids. The tab gets `${idPrefix}-tab-${key}` and
   * points at `${idPrefix}-panel-${key}`, which the page must render with the
   * matching id (and role="tabpanel").
   */
  idPrefix: string
  className?: string
}

export default function TabStrip({
  items, active, onChange, ariaLabel, idPrefix, className,
}: TabStripProps) {
  const move = (delta: number) => {
    const i = items.findIndex(it => it.key === active)
    if (i < 0 || items.length === 0) return
    onChange(items[(i + delta + items.length) % items.length].key)
  }

  return (
    <div role="tablist" aria-label={ariaLabel} className={`${s.strip} ${className ?? ''}`}>
      {items.map(item => {
        const selected = item.key === active
        return (
          <button
            key={item.key}
            type="button"
            role="tab"
            id={`${idPrefix}-tab-${item.key}`}
            aria-selected={selected}
            aria-controls={`${idPrefix}-panel-${item.key}`}
            tabIndex={selected ? 0 : -1}
            className={`tab-btn ${selected ? 'tab-active' : ''}`}
            onClick={() => onChange(item.key)}
            onKeyDown={e => {
              if (e.key === 'ArrowRight') move(1)
              else if (e.key === 'ArrowLeft') move(-1)
              else if (e.key === 'Home') onChange(items[0].key)
              else if (e.key === 'End') onChange(items[items.length - 1].key)
              else return
              e.preventDefault()
            }}
          >
            {item.label}
            {item.count !== undefined && item.count !== null && (
              <span className={s.badge}>{item.count}</span>
            )}
          </button>
        )
      })}
    </div>
  )
}
