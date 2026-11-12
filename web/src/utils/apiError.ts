// Minimal structural type so this module does not depend on i18next's own types.
type Translate = (key: string) => string

// The backend now answers domain questions with domain sentences
// (`project not found`, `note not found`, …), but a storage-level failure can
// still reach a caller as raw SQLite text — `constraint failed: FOREIGN KEY
// constraint failed (787)`, `sql: no rows in result set`,
// `UNIQUE constraint failed: projects.root_path (2067)`. Showing those verbatim
// inside a Chinese UI is both unreadable and unactionable, so every write path
// funnels its error through here first.
//
// Order matters: a domain prefix wins even when the wrapped storage error is
// still attached (`project not found: sql: no rows in result set` must read as
// "project not found", not as the generic "record is gone").
const RULES: ReadonlyArray<readonly [RegExp, string]> = [
  [/^note not found/i, 'errors.noteNotFound'],
  [/^repository not found/i, 'errors.repoNotFound'],
  [/^project not found/i, 'errors.projectNotFound'],
  [/^title is required/i, 'errors.titleRequired'],
  [/merge up requires explicit confirmation/i, 'errors.mergeNeedsConfirmation'],
  // The optimistic-lock miss (ADR-0057 D2). It arrives as
  // "note 42: expected version 3: modified by another writer", whose raw text
  // tells the user nothing about what to do next — and retrying, which is what
  // "请重试" would suggest, fails forever.
  [/modified by another writer/i, 'errors.noteConflict'],
  [/UNIQUE constraint failed: projects\.root_path/i, 'errors.pathTaken'],
  [/FOREIGN KEY constraint failed/i, 'errors.projectNotFound'],
  [/no rows in result set/i, 'errors.recordGone'],
]

/** Turn a storage/domain error into text the user can act on. An error the
 *  rules do not recognise gets an actionable sentence first, then the original
 *  message in parentheses: the sentence tells the user what to do, the detail
 *  keeps a bug report diagnosable. Dropping the detail entirely (one earlier
 *  version did) turned every unknown failure into the same sentence, which is
 *  unactionable in the opposite direction. */
export function describeApiError(t: Translate, err: unknown): string {
  const raw = err instanceof Error ? err.message : typeof err === 'string' ? err : ''
  const message = raw.trim()
  if (!message) return ''
  for (const [pattern, key] of RULES) {
    if (pattern.test(message)) return t(key)
  }
  return `${t('errors.unknownActionable')} (${message})`
}
