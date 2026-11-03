import type { SVGProps } from 'react'

export type IconName =
  | 'arrow-down'
  | 'arrow-up'
  | 'book'
  | 'bot'
  | 'check'
  | 'close'
  | 'download'
  | 'edit'
  | 'file-text'
  | 'globe'
  | 'grid'
  | 'grip'
  | 'help'
  | 'home'
  | 'layers'
  | 'lightbulb'
  | 'minus'
  | 'panel-right'
  | 'pin'
  | 'plus'
  | 'refresh'
  | 'refresh-partial'
  | 'search'
  | 'settings'
  | 'star'
  | 'trash'
  | 'warning'
  | 'zap'
  | 'sun'
  | 'moon'
  | 'monitor'

const paths: Record<IconName, string> = {
  'arrow-down': '<polyline points="6 9 12 15 18 9" />',
  'arrow-up': '<polyline points="6 15 12 9 18 15" />',
  // An open book — the knowledge base. Distinct from 'file-text' (a single
  // page) and 'layers' (a stack), so the rail reads as three distinct places.
  book: '<path d="M2 4.5A2.5 2.5 0 0 1 4.5 2H11v18H4.5A2.5 2.5 0 0 0 2 22z" /><path d="M22 4.5A2.5 2.5 0 0 0 19.5 2H13v18h6.5a2.5 2.5 0 0 0 2.5-2z" />',
  // A robot head with an antenna — the floating agent entry. The eyes are
  // dots (the "h.01" trick the help icon uses), because an outlined circle
  // at 22px reads as a second ring rather than a pupil.
  bot: '<rect x="3" y="8" width="18" height="13" rx="3" /><path d="M12 8V5" /><path d="M12 3.5h.01" /><path d="M9 14h.01" /><path d="M15 14h.01" /><path d="M9.5 18h5" /><path d="M1.5 13v4M22.5 13v4" />',
  // Confirms a completed action inline (copy-to-clipboard). The alternative —
  // swapping only the button's title attribute — is feedback the user never
  // sees, because the tooltip only renders on hover.
  check: '<polyline points="20 6 9 17 4 12" />',
  close: '<path d="M18 6 6 18M6 6l12 12" />',
  // A download arrow into a tray — the update download's icon.
  download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" /><polyline points="7 10 12 15 17 10" /><line x1="12" y1="15" x2="12" y2="3" />',
  // A pencil — renaming a topic in place.
  edit: '<path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" /><path d="M18.5 2.5a2.12 2.12 0 0 1 3 3L12 15l-4 1 1-4z" />',
  // Stacked sheets — the topic layer between a project and its notes.
  layers: '<polygon points="12 2 2 7 12 12 22 7 12 2" /><polyline points="2 17 12 22 22 17" /><polyline points="2 12 12 17 22 12" />',
  'file-text': '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /><polyline points="14 2 14 8 20 8" /><path d="M8 13h8M8 17h5" />',
  globe: '<circle cx="12" cy="12" r="10" /><path d="M2 12h20" /><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />',
  grip: '<circle cx="9" cy="6" r="1" /><circle cx="15" cy="6" r="1" /><circle cx="9" cy="12" r="1" /><circle cx="15" cy="12" r="1" /><circle cx="9" cy="18" r="1" /><circle cx="15" cy="18" r="1" />',
  grid: '<rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" />',
  help: '<circle cx="12" cy="12" r="10" /><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" /><path d="M12 17h.01" />',
  // A pitched roof over a body — the first screen. The house reads at 18px,
  // which the smaller glyphs (grid, file-text) do not always.
  home: '<path d="M3 10.5 12 3l9 7.5" /><path d="M5 9.5V21h14V9.5" />',
  // A frame split off to the right — the context rail, and the handle that
  // brings it back when collapsed.
  'panel-right': '<rect x="3" y="4" width="18" height="16" rx="2" /><line x1="15" y1="4" x2="15" y2="20" />',
  lightbulb: '<path d="M9 18h6" /><path d="M10 22h4" /><path d="M12 2a7 7 0 0 0-4 12.7c.6.5 1 1.3 1 2.1V18h6v-1.2c0-.8.4-1.6 1-2.1A7 7 0 0 0 12 2z" />',
  minus: '<path d="M5 12h14" />',
  pin: '<path d="M12 17v5M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z" />',
  plus: '<path d="M12 5v14M5 12h14" />',
  refresh: '<path d="M3 12a9 9 0 0 1 15-6.7L21 8M21 3v5h-5" />',
  'refresh-partial': '<path d="M21 12a9 9 0 1 1-6.219-8.56" />',
  search: '<circle cx="11" cy="11" r="8" /><path d="m21 21-4.3-4.3" />',
  settings: '<circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0 1.82-.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />',
  star: '<polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />',
  // A trash can — deleting a topic. The notes filed under it are kept, so the
  // icon warns about the label, not about knowledge.
  trash: '<polyline points="3 6 5 6 21 6" /><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" /><line x1="10" y1="11" x2="10" y2="17" /><line x1="14" y1="11" x2="14" y2="17" />',
  warning: '<path d="m10.29 3.86-8.2 14.14A2 2 0 0 0 3.82 21h16.36a2 2 0 0 0 1.73-3l-8.2-14.14a2 2 0 0 0-3.42 0z" />',
  zap: '<polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />',
  sun: '<circle cx="12" cy="12" r="4"/><line x1="12" y1="2" x2="12" y2="6"/><line x1="12" y1="18" x2="12" y2="22"/><line x1="2" y1="12" x2="6" y2="12"/><line x1="18" y1="12" x2="22" y2="12"/><line x1="5" y1="5" x2="7" y2="7"/><line x1="17" y1="17" x2="19" y2="19"/><line x1="7" y1="17" x2="5" y2="19"/><line x1="19" y1="5" x2="17" y2="7"/>',
  moon: '<circle cx="12" cy="12" r="6"/><circle cx="12" cy="12" r="2"/>',
  monitor: '<rect x="3" y="4" width="18" height="10" rx="2"/><rect x="9" y="14" width="6" height="4"/><line x1="12" y1="14" x2="12" y2="18"/>',
}

interface IconProps extends Omit<SVGProps<SVGSVGElement>, 'name'> {
  name: IconName
  size?: number
  filled?: boolean
}

/** Single source for the app's stroked, geometric UI icon style. */
export default function Icon({ name, size = 16, filled = false, ...props }: IconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill={filled ? 'currentColor' : 'none'}
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      {...props}
      dangerouslySetInnerHTML={{ __html: paths[name] }}
    />
  )
}

/** Renders the same icon geometry for non-React HTML, such as Markdown callouts. */
export function iconMarkup(name: IconName, size = 15): string {
  return `<svg class="app-icon" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name]}</svg>`
}