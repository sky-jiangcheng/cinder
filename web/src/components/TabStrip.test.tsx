// @vitest-environment jsdom
// TabStrip.test.tsx — the one tab implementation (ADR-0039 decision 5).
//
// What this pins:
//   - the strip is a REAL tablist: role="tablist" / role="tab" /
//     aria-selected / aria-controls, and the ids line up with the panel the
//     page renders (`${idPrefix}-tab-${key}` → `${idPrefix}-panel-${key}`);
//   - it is operable from the keyboard alone: roving tabindex plus
//     Arrow / Home / End;
//   - the count badge renders a number when there is one, renders 0 when the
//     count is zero ("Pending 0" says "the queue is empty" — a missing badge
//     says nothing), and renders nothing when the count is null.
//
// The badge is not decoration: tabbing hides everything outside the active
// tab, so a count is the only way a user learns a tab has something in it.
//
// The assertions are plain DOM reads, not jest-dom matchers: this repo does
// not install @testing-library/jest-dom, and its other component tests read
// attributes the same way.

import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import TabStrip, { type TabItem } from './TabStrip'

const items: TabItem[] = [
  { key: 'pending', label: '待审核', count: 5 },
  { key: 'merge', label: '合并建议', count: 0 },
  { key: 'scope', label: '页面作用域', count: null },
]

function renderStrip(over: Partial<React.ComponentProps<typeof TabStrip>> = {}) {
  const onChange = vi.fn()
  render(
    <TabStrip
      items={items}
      active="pending"
      onChange={onChange}
      ariaLabel="审核视图"
      idPrefix="review"
      {...over}
    />,
  )
  return { onChange }
}

describe('TabStrip', () => {
  it('renders one tab per item inside a labelled tablist', () => {
    renderStrip()
    expect(screen.getByRole('tablist').getAttribute('aria-label')).toBe('审核视图')
    expect(screen.getAllByRole('tab')).toHaveLength(3)
    expect(screen.getByRole('tab', { name: /待审核/ })).toBeTruthy()
  })

  it('marks the active tab selected and points every tab at its panel', () => {
    renderStrip()
    const pending = screen.getByRole('tab', { name: /待审核/ })
    const merge = screen.getByRole('tab', { name: /合并建议/ })
    expect(pending.getAttribute('aria-selected')).toBe('true')
    expect(merge.getAttribute('aria-selected')).toBe('false')
    expect(pending.getAttribute('id')).toBe('review-tab-pending')
    expect(pending.getAttribute('aria-controls')).toBe('review-panel-pending')
    expect(merge.getAttribute('aria-controls')).toBe('review-panel-merge')
  })

  it('keeps the roving tabindex: only the active tab is tabbable', () => {
    renderStrip()
    expect(screen.getByRole('tab', { name: /待审核/ }).getAttribute('tabindex')).toBe('0')
    expect(screen.getByRole('tab', { name: /合并建议/ }).getAttribute('tabindex')).toBe('-1')
  })

  it('reports the chosen key on click', () => {
    const { onChange } = renderStrip()
    fireEvent.click(screen.getByRole('tab', { name: /合并建议/ }))
    expect(onChange).toHaveBeenCalledWith('merge')
  })

  it('walks the strip with the arrow keys', () => {
    const { onChange } = renderStrip()
    fireEvent.keyDown(screen.getByRole('tab', { name: /待审核/ }), { key: 'ArrowRight' })
    expect(onChange).toHaveBeenLastCalledWith('merge')
    fireEvent.keyDown(screen.getByRole('tab', { name: /待审核/ }), { key: 'ArrowLeft' })
    // Wraps around to the last item.
    expect(onChange).toHaveBeenLastCalledWith('scope')
  })

  it('jumps to the ends with Home and End', () => {
    const { onChange } = renderStrip()
    fireEvent.keyDown(screen.getByRole('tab', { name: /待审核/ }), { key: 'End' })
    expect(onChange).toHaveBeenLastCalledWith('scope')
    fireEvent.keyDown(screen.getByRole('tab', { name: /待审核/ }), { key: 'Home' })
    expect(onChange).toHaveBeenLastCalledWith('pending')
  })

  it('shows the count badge, including a zero count', () => {
    renderStrip()
    expect(screen.getByRole('tab', { name: /待审核/ }).textContent).toContain('5')
    // Zero is information ("the queue is empty"); a missing badge says nothing.
    expect(screen.getByRole('tab', { name: /合并建议/ }).textContent).toContain('0')
  })

  it('omits the badge when there is no count', () => {
    renderStrip()
    expect(screen.getByRole('tab', { name: /页面作用域/ }).textContent).toBe('页面作用域')
  })
})
