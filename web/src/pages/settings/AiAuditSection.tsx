import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getAIAuditStatus, listAICallAudit, updateConfig } from '../../api/client'
import type { AICallLogEntry, AIAuditStatus } from '../../api/types'
import css from './AiAuditSection.module.css'

interface Props {
  /** Flat key/value config record (data.config from GetConfig). */
  config: Record<string, string>
  showMessage: (msg: string) => void
}

/**
 * AI call audit and content review (ADR-0030).
 *
 * This is the desktop half of the "security plugin" — the same capability
 * the agent reaches through repotory_ai_audit, rendered for the person who
 * turned it on.
 *
 * Three things are deliberately visible here, because hiding them would
 * make the feature dishonest:
 *
 *  - Everything is OFF by default, and the switches say so.
 *  - The trail stores a HASH and a SIZE, never the prompt or the reply. The
 *    table below shows the hash so the user can see that is what is kept.
 *  - The rules are the user's own regexes. Cinder ships no blocklist,
 *    because a keyword list is a weak control — the panel says so rather
 *    than implying coverage it does not have.
 */
export default function AiAuditSection({ config, showMessage }: Props) {
  const { t } = useTranslation()
  const [auditEnabled, setAuditEnabled] = useState(config.ai_audit_enabled === '1')
  const [reviewInput, setReviewInput] = useState(config.ai_review_input === '1')
  const [reviewOutput, setReviewOutput] = useState(config.ai_review_output === '1')
  const [rules, setRules] = useState(config.ai_review_rules || '')
  const [status, setStatus] = useState<AIAuditStatus | null>(null)
  const [calls, setCalls] = useState<AICallLogEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  // Loads the status and the recent calls. It deliberately does NOT touch
  // state itself: the setState calls live in the promise chain at each call
  // site, so the react-hooks/set-state-in-effect rule stays satisfied (a
  // synchronous setState inside an effect body cascades renders).
  const load = useCallback(async (): Promise<{ status: AIAuditStatus | null; calls: AICallLogEntry[] }> => {
    try {
      const s = await getAIAuditStatus()
      const rows = await listAICallAudit(20)
      return { status: s, calls: Array.isArray(rows) ? rows : [] }
    } catch {
      // A backend without the audit table (pre-v28) simply has nothing to
      // show; that is not an error worth a banner.
      return { status: null, calls: [] }
    }
  }, [])

  const refresh = useCallback(() => {
    return load().then(({ status: s, calls: rows }) => {
      setStatus(s)
      setCalls(rows)
      setLoading(false)
    })
  }, [load])

  useEffect(() => {
    // The setState calls happen inside the promise chain (refresh -> load ->
    // .then), not synchronously in the effect body, so this does not cascade
    // renders (react-hooks/set-state-in-effect).
    void refresh()
  }, [refresh])

  const save = async (patch: Record<string, string>) => {
    setSaving(true)
    try {
      for (const [key, value] of Object.entries(patch)) {
        await updateConfig(key, value)
      }
      showMessage(t('settings.audit.saved', { defaultValue: 'AI 审计设置已保存' }))
      await refresh()
    } catch (e) {
      showMessage(`error: ${e instanceof Error ? e.message : String(e)}`)
    } finally {
      setSaving(false)
    }
  }

  const toggleAudit = (next: boolean) => {
    setAuditEnabled(next)
    void save({ ai_audit_enabled: next ? '1' : '0' })
  }
  const toggleInput = (next: boolean) => {
    setReviewInput(next)
    void save({ ai_review_input: next ? '1' : '0' })
  }
  const toggleOutput = (next: boolean) => {
    setReviewOutput(next)
    void save({ ai_review_output: next ? '1' : '0' })
  }
  const saveRules = () => {
    void save({ ai_review_rules: rules })
  }

  return (
    <div className={css.auditSection} data-testid="ai-audit-section">
      <h2>{t('settings.audit.title', { defaultValue: 'AI 调用审计与内容审查' })}</h2>
      <p className={css.auditIntro}>
        {t('settings.audit.intro', {
          defaultValue:
            '记录本机发往外部模型的调用（时间、端点、模型、来源面、字符数），并在发出前按你自己的规则审查提示词。默认全部关闭；关闭时行为与之前完全一致。',
        })}
      </p>

      <div className={`toggle-row ${css.auditToggle}`}>
        <label htmlFor="audit-enabled">
          {t('settings.audit.enableAudit', { defaultValue: '记录调用审计' })}
        </label>
        <input
          id="audit-enabled"
          type="checkbox"
          checked={auditEnabled}
          disabled={saving}
          onChange={(e) => toggleAudit(e.target.checked)}
        />
      </div>
      <p className={css.auditHint}>
        {t('settings.audit.auditHint', {
          defaultValue:
            '每次调用写入一条记录：时间、端点、模型、来源（MCP / HTTP / 桌面）、提示词哈希与字符数、审查判定。',
        })}
      </p>

      <div className={`toggle-row ${css.auditToggle}`}>
        <label htmlFor="audit-input">
          {t('settings.audit.enableInput', { defaultValue: '发出前审查提示词' })}
        </label>
        <input
          id="audit-input"
          type="checkbox"
          checked={reviewInput}
          disabled={saving}
          onChange={(e) => toggleInput(e.target.checked)}
        />
      </div>

      <div className={`toggle-row ${css.auditToggle}`}>
        <label htmlFor="audit-output">
          {t('settings.audit.enableOutput', { defaultValue: '到达时审查回复' })}
        </label>
        <input
          id="audit-output"
          type="checkbox"
          checked={reviewOutput}
          disabled={saving}
          onChange={(e) => toggleOutput(e.target.checked)}
        />
      </div>
      <p className={css.auditHint}>
        {t('settings.audit.outputHint', {
          defaultValue:
            '流式回复的头部已经显示在屏幕上，无法撤回；命中后只截断后续内容，判定记为「已标记」而非「已拦截」。',
        })}
      </p>

      <div className="form-group">
        <label htmlFor="audit-rules">
          {t('settings.audit.rules', { defaultValue: '审查规则（每行一个正则，# 开头为注释）' })}
        </label>
        <textarea
          id="audit-rules"
          value={rules}
          onChange={(e) => setRules(e.target.value)}
          className={`form-input ${css.auditRules}`}
          rows={4}
          placeholder={t('settings.audit.rulesPlaceholder', {
            defaultValue: '身份证\n客户手机号\n-----BEGIN',
          })}
        />
      </div>
      <p className={css.auditHint}>
        {t('settings.audit.rulesHint', {
          defaultValue:
            '规则由你自己写，Cinder 不内置任何词表——关键词匹配是很弱的控制，容易被改写绕过，也会误伤正常内容。规则不区分大小写；写错的正则会被跳过并在日志中提示。',
        })}
      </p>

      <div className="empty-actions">
        <button className="btn btn-primary" onClick={saveRules} disabled={saving}>
          {saving
            ? t('settings.saving', { defaultValue: 'Saving…' })
            : t('settings.save', { defaultValue: 'Save' })}
        </button>
        <button className="btn btn-secondary" onClick={() => void refresh()} disabled={loading}>
          {loading
            ? t('settings.audit.refreshing', { defaultValue: '刷新中…' })
            : t('settings.audit.refresh', { defaultValue: '刷新记录' })}
        </button>
      </div>

      {status && (
        <div className={css.auditStats} data-testid="ai-audit-stats">
          <span>
            {t('settings.audit.statCalls', { defaultValue: '已记录' })}: {status.call_count}
          </span>
          <span>
            {t('settings.audit.statBlocked', { defaultValue: '已拦截' })}: {status.blocked_count}
          </span>
          <span>
            {t('settings.audit.statFlagged', { defaultValue: '已标记' })}: {status.flagged_count}
          </span>
          <span>
            {t('settings.audit.statRules', { defaultValue: '规则数' })}: {status.rule_count}
          </span>
          <span>
            {t('settings.audit.statSurface', { defaultValue: '当前来源面' })}: {status.surface}
          </span>
        </div>
      )}
      {status && (
        <p className={css.auditHint}>
          {t('settings.audit.noFullText', {
            defaultValue:
              '记录只保存提示词的 SHA-256 哈希与字符数，不保存提示词与回复原文——避免把密钥与业务数据写进知识库所在的同一个数据库。',
          })}
          {' '}
          {status.retention === 'append_only'
            ? t('settings.audit.retentionAppendOnly', {
                defaultValue: '审计记录只增不删，不会自动清理；如需移除请手动删除数据库。',
              })
            : null}
        </p>
      )}

      {calls.length > 0 && (
        /* Seven columns do not fit a narrow settings pane: without this
         * wrapper the last one (the verdict) is simply clipped by the viewport,
         * with nothing to scroll. The wrapper scrolls the table alone so the
         * page itself never grows a horizontal scrollbar. */
        <div className={css.auditTableWrap}>
          <table className={css.auditTable} data-testid="ai-audit-table">
          <thead>
            <tr>
              <th>{t('settings.audit.colTime', { defaultValue: '时间' })}</th>
              <th>{t('settings.audit.colSurface', { defaultValue: '来源' })}</th>
              <th>{t('settings.audit.colEndpoint', { defaultValue: '端点' })}</th>
              <th>{t('settings.audit.colModel', { defaultValue: '模型' })}</th>
              <th>{t('settings.audit.colChars', { defaultValue: '字符数' })}</th>
              <th>{t('settings.audit.colHash', { defaultValue: '提示词哈希' })}</th>
              <th>{t('settings.audit.colVerdict', { defaultValue: '判定' })}</th>
            </tr>
          </thead>
          <tbody>
            {calls.map(c => (
              <tr key={c.id}>
                <td className={css.auditTime}>{c.created_at}</td>
                <td>{c.surface}</td>
                {/* The endpoint is the single most important column here: "what
                    left this machine" is only half the question, "where to" is
                    the other half. Long URLs are truncated in CSS with the
                    full value on hover, so the column cannot blow the table
                    width out on a long gateway path. */}
                <td className={css.auditEndpoint} title={c.endpoint}>
                  {c.endpoint || '—'}
                </td>
                <td>{c.model}</td>
                <td>
                  {c.prompt_chars}
                  {c.response_chars > 0 ? ` / ${c.response_chars}` : ''}
                </td>
                {/* Only a prefix: the full digest is long enough to wrap and
                    says nothing more at a glance. The tooltip carries the
                    whole thing, which is what makes the "we keep a hash, not
                    the text" promise checkable by the person reading it. */}
                <td className={css.auditHash} title={c.prompt_hash}>
                  {c.prompt_hash ? c.prompt_hash.slice(0, 8) : '—'}
                </td>
                <td>
                  {c.input_verdict === 'blocked'
                    ? t('settings.audit.verdictBlocked', { defaultValue: '已拦截' })
                    : c.output_verdict === 'flagged'
                      ? t('settings.audit.verdictFlagged', { defaultValue: '已标记' })
                      : c.input_verdict === 'allow'
                        ? t('settings.audit.verdictAllow', { defaultValue: '通过' })
                        : '—'}
                </td>
              </tr>
            ))}
          </tbody>
          </table>
        </div>
      )}
      {status && status.call_count === 0 && (
        <p className={css.auditHint}>
          {t('settings.audit.empty', {
            defaultValue: '还没有调用记录。开启「记录调用审计」后，每次发往外部模型的调用都会在这里留下一行。',
          })}
        </p>
      )}
    </div>
  )
}
