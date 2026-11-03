import { type ReactNode, type Ref } from 'react'
import s from './PageShell.module.css'

// The page shell: a pinned header above a scrolling body.
//
// Why this exists: the shell's own scroll container (`.mainContent`) used to
// hold EVERYTHING — the toolbar, the tab strip, and the content. That made the
// tab strip scroll away with the content, and it made the scrollbar's track
// start at the top of the column, ABOVE the tab strip: a reviewer could scroll
// the strip off the screen and the scrollbar still said "you are at the top".
//
// The fix is structural, not a magic offset: the chrome (toolbar + tab strip)
// moves OUT of the scrolling region, so the scrolling region starts under the
// tab strip. The scrollbar's top edge is then the tab strip's bottom edge —
// the strip stays put, and nothing scrolls above it.
//
// One shell, five pages (dashboard / project detail / knowledge / review /
// settings) — the same skeleton, so "where does this page scroll?" has one
// answer in the whole product.

interface PageShellProps {
  /**
   * The pinned chrome: toolbar, tab strip, banners. It never scrolls.
   * Rendered above the scrolling body; the body's top edge is the header's
   * bottom edge.
   */
  header?: ReactNode
  /** The scrolling body. */
  children: ReactNode
  /**
   * Ref to the scrolling region. A tab switch resets it to the top (the new
   * panel starts at its own beginning, not at the old scroll offset).
   */
  bodyRef?: Ref<HTMLDivElement>
  /**
   * Page-level content that lives BELOW the scrolling region — a status bar.
   * It stays put; only the body scrolls.
   */
  footer?: ReactNode
  /** Extra class on the shell root, for page-specific tweaks. */
  className?: string
}

export default function PageShell({ header, children, bodyRef, footer, className }: PageShellProps) {
  return (
    <div className={`${s.page} ${className ?? ''}`}>
      {header ? <div className={s.fixed}>{header}</div> : null}
      <div className={s.scroll} ref={bodyRef}>
        {children}
      </div>
      {footer ? <div className={s.footer}>{footer}</div> : null}
    </div>
  )
}
