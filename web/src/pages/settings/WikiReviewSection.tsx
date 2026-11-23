import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { updateConfig } from '../../api/client'

interface Props {
  /** Flat key/value config record (data.config from GetConfig). */
  config: Record<string, string>
  showMessage: (msg: string) => void
}

/**
 * AI-assisted review of compiled knowledge pages.
 *
 * This is a separate block, not a fourth settings tab: it extends the same AI
 * surface the compile and lint gates already live on. All three values are
 * saved together so the user cannot end up with auto-approval on while its
 * required AI annotation is off.
 */
export default function WikiReviewSection({ config, showMessage }: Props) {
  const { t } = useTranslation()
  const [ai, setAi] = useState(config.wiki_review_ai === '1')
  const [auto, setAuto] = useState(config.wiki_review_auto === '1')
  const [timeout, setTimeout] = useState(config.wiki_review_timeout_hours || '0')
  const [saving, setSaving] = useState(false)

  const save = async () => {
    setSaving(true)
    try {
      await updateConfig('wiki_review_ai', ai ? '1' : '0')
      await updateConfig('wiki_review_auto', auto ? '1' : '0')
      await updateConfig('wiki_review_timeout_hours', String(Math.max(0, Number(timeout) || 0)))
      showMessage(t('settings.wikiReview.saved'))
    } catch (e: unknown) {
      showMessage(t('settings.saveFailedMsg', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div data-testid="wiki-review-section">
      <h2>{t('settings.wikiReview.title')}</h2>
      <p className="section-desc">{t('settings.wikiReview.desc')}</p>

      <div className="form-group">
        <label htmlFor="wiki-review-ai">
          <input
            id="wiki-review-ai"
            type="checkbox"
            checked={ai}
            onChange={e => setAi(e.target.checked)}
          />{' '}
          {t('settings.wikiReview.ai')}
        </label>
        <span className="form-hint">{t('settings.wikiReview.aiHint')}</span>
      </div>

      <div className="form-group">
        <label htmlFor="wiki-review-auto">
          <input
            id="wiki-review-auto"
            type="checkbox"
            checked={auto}
            onChange={e => setAuto(e.target.checked)}
          />{' '}
          {t('settings.wikiReview.auto')}
        </label>
        <span className="form-hint">{t('settings.wikiReview.autoHint')}</span>
      </div>

      <div className="form-group">
        <label htmlFor="wiki-review-timeout">{t('settings.wikiReview.timeout')}</label>
        <input
          id="wiki-review-timeout"
          type="number"
          className="form-input"
          min={0}
          max={8760}
          value={timeout}
          onChange={e => setTimeout(e.target.value)}
        />
        <span className="form-hint">{t('settings.wikiReview.timeoutHint')}</span>
      </div>

      <div className="empty-actions">
        <button className="btn btn-primary" onClick={save} disabled={saving}>
          {saving ? t('settings.saving', { defaultValue: 'Saving…' }) : t('settings.save', { defaultValue: 'Save' })}
        </button>
      </div>
    </div>
  )
}
