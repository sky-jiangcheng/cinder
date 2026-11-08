import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getReviewConfig, updateReviewConfig } from '../../api/client'
import type { ReviewConfig } from '../../api/client'

// Settings → 每日审查 (ADR-0053 D5).
//
// The panel exists because the feature is OFF by default and must stay that
// way: the job reads the user's source code and, with AI on, sends excerpts to
// whatever endpoint is configured in the AI tab. A default-on version of this
// would be a silent egress path, so the first control on the panel is the
// master switch and the reason it is off is spelled out next to it.
//
// Everything is saved by one button rather than per-field: these six values
// only make sense together (a time with no scope, an AI toggle with the master
// switch off), and half-saved combinations would leave the ticker running
// against a schedule the user did not intend.

interface Props {
  showMessage: (msg: string) => void
}

export default function ReviewTab({ showMessage }: Props) {
  const { t } = useTranslation()
  const [cfg, setCfg] = useState<ReviewConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    getReviewConfig()
      .then(setCfg)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : t('common.failed')))
      .finally(() => setLoading(false))
  }, [t])

  const patch = (next: Partial<ReviewConfig>) => setCfg(c => (c ? { ...c, ...next } : c))

  const save = async () => {
    if (!cfg) return
    setSaving(true)
    try {
      await updateReviewConfig(cfg)
      showMessage(t('settings.review.saved'))
    } catch (e: unknown) {
      showMessage(t('settings.saveFailedMsg', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return <div className="skeleton skeleton-text" style={{ width: '100%', height: 120 }} />
  }
  if (error || !cfg) {
    return <p className="form-hint">{error || t('common.failed')}</p>
  }

  return (
    <div className="settings-section">
      <h2>{t('settings.review.title')}</h2>
      <p className="form-hint">{t('settings.review.desc')}</p>

      <div className="form-group">
        <label htmlFor="review-enabled">
          <input
            id="review-enabled"
            type="checkbox"
            checked={cfg.enabled}
            onChange={e => patch({ enabled: e.target.checked })}
          />{' '}
          {t('settings.review.enabled')}
        </label>
        <span className="form-hint">{t('settings.review.enabledHint')}</span>
      </div>

      <div className="form-group">
        <label htmlFor="review-at">{t('settings.review.at')}</label>
        <input
          id="review-at"
          type="time"
          className="form-input"
          value={cfg.at}
          onChange={e => patch({ at: e.target.value })}
        />
        <span className="form-hint">{t('settings.review.atHint')}</span>
      </div>

      <div className="form-group">
        <label htmlFor="review-scope">{t('settings.review.scope')}</label>
        <input
          id="review-scope"
          type="text"
          className="form-input"
          value={cfg.scope}
          onChange={e => patch({ scope: e.target.value })}
          placeholder="collected"
        />
        <span className="form-hint">{t('settings.review.scopeHint')}</span>
      </div>

      <div className="form-group">
        <label htmlFor="review-ai">
          <input
            id="review-ai"
            type="checkbox"
            checked={cfg.ai}
            onChange={e => patch({ ai: e.target.checked })}
          />{' '}
          {t('settings.review.ai')}
        </label>
        <span className="form-hint">{t('settings.review.aiHint')}</span>
      </div>

      <div className="form-group">
        <label htmlFor="review-notify">{t('settings.review.notify')}</label>
        <select
          id="review-notify"
          className="form-input"
          value={cfg.notify}
          onChange={e => patch({ notify: e.target.value as ReviewConfig['notify'] })}
        >
          <option value="high">{t('settings.review.notifyHigh')}</option>
          <option value="all">{t('settings.review.notifyAll')}</option>
          <option value="off">{t('settings.review.notifyOff')}</option>
        </select>
        <span className="form-hint">{t('settings.review.notifyHint')}</span>
      </div>

      <div className="form-group">
        <label htmlFor="review-max-diff">{t('settings.review.maxDiff')}</label>
        <input
          id="review-max-diff"
          type="number"
          className="form-input"
          min={100}
          max={20000}
          value={cfg.max_diff_lines}
          onChange={e => patch({ max_diff_lines: Number(e.target.value) || 0 })}
        />
        <span className="form-hint">{t('settings.review.maxDiffHint')}</span>
      </div>

      <div className="form-group">
        <span>{t('settings.review.rules')}</span>
        {Object.keys(cfg.rules).sort().map(id => (
          <label key={id} htmlFor={`review-rule-${id}`} style={{ display: 'block' }}>
            <input
              id={`review-rule-${id}`}
              type="checkbox"
              checked={!!cfg.rules[id]}
              onChange={e => patch({ rules: { ...cfg.rules, [id]: e.target.checked } })}
            />{' '}
            <code>{id}</code>
          </label>
        ))}
        <span className="form-hint">{t('settings.review.rulesHint')}</span>
      </div>

      <button className="btn btn-primary" onClick={save} disabled={saving}>
        {t('settings.saveConfig')}
      </button>
    </div>
  )
}
