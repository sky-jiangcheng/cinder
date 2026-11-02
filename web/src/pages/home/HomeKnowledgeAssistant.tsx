import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../../components/Icon'
import {
  askAIWithEvidenceStream,
  searchAll,
  createNoteWithMeta,
  fileAnswerAsPage,
  getConfig,
  type Project,
  type Evidence,
  type EvidenceItem,
  type SearchHit,
} from '../../api/client'
import { renderMarkdown } from '../../utils/markdown'
import type { PushToast } from '../../hooks/useToast'
import s from './HomeKnowledgeAssistant.module.css'

export interface DialogueMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  timestamp: string
  scopeProjectId?: number
  scopeProjectName?: string
  isStreaming?: boolean
  evidence?: Evidence
  projectCards?: Project[]
  knowledgeCards?: EvidenceItem[]
}

interface Props {
  projects: Project[]
  onToast: PushToast
  onGoToSettings: () => void
}

// Local (no-AI) fallback cards come from SearchAll, which mixes notes and todos,
// so the hit's own type is carried through rather than flattened to 'note'.
export function cardFromSearchHit(hit: SearchHit, index: number, projectName?: string): EvidenceItem {
  return {
    ref: hit.type === 'note' ? `N${index + 1}` : `T${index + 1}`,
    type: hit.type,
    id: hit.id,
    project_id: hit.project_id,
    project_name: projectName,
    title: hit.title,
    kind: hit.type === 'note' ? 'knowledge' : 'todo',
    snippet: hit.snippet,
    rank: index + 1,
  }
}

// A todo has no per-row deep link — ProjectDetail only focuses ?noteId=, so an
// id there would open the notes tab and highlight the note that happens to
// share the number. Landing on the todos tab is the same rule the Knowledge
// page uses (Knowledge.tsx).
export function knowledgeHref(item: EvidenceItem, fallbackProjectId: number): string {
  if (item.type === 'page') {
    return item.project_id && item.project_id > 0 ? `/project/${item.project_id}` : '/review'
  }
  const pid = item.project_id && item.project_id > 0 ? item.project_id : fallbackProjectId
  return item.type === 'todo'
    ? `/project/${pid}?tab=todos`
    : `/project/${pid}?tab=notes&noteId=${item.id}`
}

export default function HomeKnowledgeAssistant({ projects, onToast, onGoToSettings }: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()

  const [scopeProjectId, setScopeProjectId] = useState<number>(0)
  const [question, setQuestion] = useState('')
  const [asking, setAsking] = useState(false)
  const [configured, setConfigured] = useState<boolean | null>(null)
  const [messages, setMessages] = useState<DialogueMessage[]>([])

  const chatStreamRef = useRef<HTMLDivElement>(null)
  const cancelStreamRef = useRef<(() => void) | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  // Check if AI chat endpoint is configured in settings
  useEffect(() => {
    let cancelled = false
    getConfig()
      .then(d => {
        if (cancelled) return
        const c = d.config || {}
        setConfigured(!!(c.ai_chat_base_url && c.ai_chat_model))
      })
      .catch(() => {
        if (!cancelled) setConfigured(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  // Auto-scroll chat stream as messages change or stream. Gated on a non-empty
  // conversation: on mount (empty welcome state) scrollHeight can still exceed
  // clientHeight by a few px, and blindly pinning to the bottom cropped the
  // welcome icon at the top of the stream.
  useEffect(() => {
    if (chatStreamRef.current && messages.length > 0) {
      chatStreamRef.current.scrollTop = chatStreamRef.current.scrollHeight
    }
  }, [messages])

  // Seconds since the current ask started — the typing dots alone give zero
  // feedback and a local model's first token can take minutes.
  const [askElapsed, setAskElapsed] = useState(0)
  useEffect(() => {
    if (!asking) {
      setAskElapsed(0)
      return
    }
    const t0 = Date.now()
    const iv = setInterval(() => setAskElapsed(Math.floor((Date.now() - t0) / 1000)), 1000)
    return () => clearInterval(iv)
  }, [asking])

  // Stop active streaming request
  const handleStop = () => {
    if (cancelStreamRef.current) {
      cancelStreamRef.current()
      cancelStreamRef.current = null
    }
    setAsking(false)
    setMessages(prev =>
      prev.map(m => (m.isStreaming ? { ...m, isStreaming: false } : m)),
    )
  }

  // Clear conversation
  const handleClear = () => {
    handleStop()
    setMessages([])
  }

  // Find relevant projects based on text matches and evidence item project IDs
  const matchProjects = (qText: string, ansText: string, evidenceItems: EvidenceItem[] = []): Project[] => {
    const matched = new Map<number, Project>()
    if (scopeProjectId > 0) {
      const p = projects.find(x => x.id === scopeProjectId)
      if (p) matched.set(p.id, p)
    }

    // Projects directly cited in evidence
    evidenceItems.forEach(it => {
      if (it.project_id && it.project_id > 0) {
        const p = projects.find(x => x.id === it.project_id)
        if (p) matched.set(p.id, p)
      }
    })

    // Projects mentioned by name in question or answer
    const qLower = qText.toLowerCase()
    const ansLower = ansText.toLowerCase()
    projects.forEach(p => {
      const pNameLower = p.name.toLowerCase()
      if (pNameLower.length >= 2 && (qLower.includes(pNameLower) || ansLower.includes(pNameLower))) {
        matched.set(p.id, p)
      }
    })

    return Array.from(matched.values()).slice(0, 6)
  }

  // Send question to knowledge base
  const handleSend = async (overridePrompt?: string) => {
    const promptText = (overridePrompt ?? question).trim()
    if (!promptText || asking) return

    setQuestion('')
    if (cancelStreamRef.current) {
      cancelStreamRef.current()
      cancelStreamRef.current = null
    }

    const targetProject = projects.find(p => p.id === scopeProjectId)
    const userMsgId = `user-${Date.now()}`
    const userMsg: DialogueMessage = {
      id: userMsgId,
      role: 'user',
      content: promptText,
      timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
      scopeProjectId,
      scopeProjectName: targetProject ? targetProject.name : undefined,
    }

    const assistantMsgId = `ai-${Date.now() + 1}`
    const initialAssistantMsg: DialogueMessage = {
      id: assistantMsgId,
      role: 'assistant',
      content: '',
      timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
      isStreaming: true,
      scopeProjectId,
    }

    setMessages(prev => [...prev, userMsg, initialAssistantMsg])
    setAsking(true)

    // Direct LLM mode when configured
    if (configured) {
      const cancel = askAIWithEvidenceStream(scopeProjectId, promptText, {
        onDelta: delta => {
          setMessages(prev =>
            prev.map(m =>
              m.id === assistantMsgId ? { ...m, content: m.content + delta } : m,
            ),
          )
        },
        onDone: res => {
          setAsking(false)
          cancelStreamRef.current = null

          const items = res.evidence?.items || []
          // Augment items with project names if missing
          const enrichedItems = items.map(it => {
            if (!it.project_name && it.project_id) {
              const p = projects.find(x => x.id === it.project_id)
              if (p) return { ...it, project_name: p.name }
            }
            return it
          })

          const matchedProjs = matchProjects(promptText, res.reply || '', enrichedItems)

          setMessages(prev =>
            prev.map(m =>
              m.id === assistantMsgId
                ? {
                    ...m,
                    isStreaming: false,
                    content: res.reply,
                    evidence: res.evidence,
                    projectCards: matchedProjs,
                    knowledgeCards: enrichedItems,
                  }
                : m,
            ),
          )
        },
        onError: err => {
          setAsking(false)
          cancelStreamRef.current = null
          const errText = err instanceof Error ? err.message : String(err)
          setMessages(prev =>
            prev.map(m =>
              m.id === assistantMsgId
                ? {
                    ...m,
                    isStreaming: false,
                    content: m.content || `${t('ai.sendFailed', { defaultValue: '请求失败' })}: ${errText}`,
                  }
                : m,
            ),
          )
          onToast({
            kind: 'error',
            title: t('ai.sendFailed', { defaultValue: '请求失败' }),
            message: errText,
          })
        },
      })
      cancelStreamRef.current = cancel
    } else {
      // Local knowledge search fallback. There is no stream to abort, but the
      // state writes must obey the same stop signal as the stream path: a
      // searchAll resolving after Stop (or after a new question took over the
      // asking slot) must not write stale cards into messages, and its
      // finally must not setAsking(false) — that would kill the replacement
      // question's spinner and elapsed timer mid-flight.
      let cancelled = false
      cancelStreamRef.current = () => { cancelled = true }
      try {
        const hits = await searchAll(promptText)
        if (cancelled) return
        const relevantHits = (hits || [])
          .filter(h => scopeProjectId === 0 || h.project_id === scopeProjectId)
          .slice(0, 8)

        const kCards: EvidenceItem[] = relevantHits.map((h, i) =>
          cardFromSearchHit(h, i, projects.find(proj => proj.id === h.project_id)?.name),
        )

        const matchedProjs = matchProjects(promptText, '', kCards)

        let replyText = t('home.fallbackNotice', {
          defaultValue: '未配置大模型端点，当前通过本地知识库全量索引进行匹配检索。可在「设置」中配置 AI 服务以获得智能总结。',
        })
        if (kCards.length === 0 && matchedProjs.length === 0) {
          replyText += `\n\n${t('home.noResults', { defaultValue: '没有找到匹配的知识或项目。' })}`
        }

        setMessages(prev =>
          prev.map(m =>
            m.id === assistantMsgId
              ? {
                  ...m,
                  isStreaming: false,
                  content: replyText,
                  projectCards: matchedProjs,
                  knowledgeCards: kCards,
                }
              : m,
          ),
        )
      } catch (err) {
        if (cancelled) return
        setMessages(prev =>
          prev.map(m =>
            m.id === assistantMsgId
              ? {
                  ...m,
                  isStreaming: false,
                  content: `${t('common.failed', { defaultValue: '检索失败' })}: ${err instanceof Error ? err.message : String(err)}`,
                }
              : m,
          ),
        )
      } finally {
        // Only the owner clears the asking slot: when cancelled, Stop already
        // set asking=false and nulled the ref, and a replacement question may
        // own both — touching them here is the race this flag exists for.
        if (!cancelled) {
          setAsking(false)
          cancelStreamRef.current = null
        }
      }
    }
  }

  // Handle keypress inside textarea: Enter sends, Shift+Enter newlines
  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void handleSend()
    }
  }

  // Copy reply content to clipboard
  const handleCopy = async (content: string) => {
    try {
      await navigator.clipboard.writeText(content)
      onToast({
        kind: 'success',
        title: t('home.replyCopied', { defaultValue: '回复已复制到剪贴板' }),
      })
    } catch {
      onToast({ kind: 'error', title: t('common.failed', { defaultValue: '复制失败' }) })
    }
  }

  // Save reply as a knowledge note
  const handleSaveAsNote = async (msg: DialogueMessage) => {
    const targetPid =
      msg.scopeProjectId && msg.scopeProjectId > 0
        ? msg.scopeProjectId
        : (msg.projectCards && msg.projectCards[0]?.id) || projects[0]?.id

    if (!targetPid) {
      onToast({ kind: 'error', title: t('knowledge.noProjectsMsg', { defaultValue: '未找到可用项目，请先添加项目' }) })
      return
    }

    try {
      const titleLine = msg.content.split('\n')[0].replace(/[#*`_]/g, '').trim().slice(0, 60) || t('home.conversationTitle')
      await createNoteWithMeta(targetPid, msg.content, {
        title: titleLine,
        kind: 'knowledge',
        source: 'ai',
      })
      onToast({
        kind: 'success',
        title: t('home.noteSaved', { defaultValue: '已成功存为知识笔记' }),
      })
    } catch (err) {
      onToast({
        kind: 'error',
        title: t('fab.saveFailed', { defaultValue: '保存失败' }),
        message: err instanceof Error ? err.message : undefined,
      })
    }
  }

  // File reply as wiki page with citations intact
  const handleFileAsPage = async (msg: DialogueMessage) => {
    const targetPid =
      msg.scopeProjectId && msg.scopeProjectId > 0
        ? msg.scopeProjectId
        : (msg.projectCards && msg.projectCards[0]?.id) || projects[0]?.id

    if (!targetPid || !msg.evidence) return

    try {
      const citedRefs = (msg.evidence.items || []).map(it => it.ref)
      const titleLine = msg.content.split('\n')[0].replace(/[#*`_]/g, '').trim().slice(0, 40) || t('home.qaTitle')
      const pageId = await fileAnswerAsPage(targetPid, titleLine, msg.content, msg.evidence, citedRefs)
      onToast({
        kind: 'success',
        title: t('ai.pageSaved', { defaultValue: '已归档为 Wiki 页面' }),
        message: t('ai.pageSavedDetail', { id: pageId, refs: citedRefs.length }),
      })
    } catch (err) {
      onToast({
        kind: 'error',
        title: t('ai.pageSaveFailed', { defaultValue: '归档失败' }),
        message: err instanceof Error ? err.message : undefined,
      })
    }
  }

  // Navigate to knowledge item detail
  const handleOpenKnowledge = (item: EvidenceItem) => {
    navigate(knowledgeHref(item, projects[0]?.id || 0))
  }

  const promptSuggestions = [
    { text: t('home.prompt1', { defaultValue: '梳理当前所有项目的核心架构与依赖' }), icon: 'lightbulb' as const },
    { text: t('home.prompt2', { defaultValue: '最近有哪些会话交接记录与重点事项？' }), icon: 'refresh' as const },
    { text: t('home.prompt3', { defaultValue: '知识库中有哪些高频架构参考与最佳实践？' }), icon: 'file-text' as const },
    { text: t('home.prompt4', { defaultValue: '当前有哪些待处理的关键待办任务？' }), icon: 'check' as const },
  ]

  return (
    <div className={s.container}>
      {/* Assistant Header */}
      <div className={s.header}>
        <div className={s.titleArea}>
          <div className={s.title}>
            <span>{t('home.assistantTitle', { defaultValue: '知识库智能对话' })}</span>
            <span className={s.badgeLive}>
              <span className={s.liveDot} />
              Live
            </span>
          </div>
          <p className={s.subtitle}>
            {t('home.assistantSub', { defaultValue: '与本地知识库、项目架构及会话记录实时互动' })}
          </p>
        </div>

        <div className={s.headerRight}>
          <select
            className={s.scopeSelect}
            value={scopeProjectId}
            onChange={e => setScopeProjectId(Number(e.target.value))}
            aria-label={t('home.scopeSelectLabel', { defaultValue: '对话检索范围' })}
          >
            <option value={0}>{t('home.scopeAll', { defaultValue: '全库知识与全部项目' })}</option>
            {projects.map(p => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>

          {messages.length > 0 && (
            <button
              type="button"
              className={s.clearBtn}
              onClick={handleClear}
              title={t('home.clearChat', { defaultValue: '清空对话' })}
            >
              <Icon name="refresh" size={13} />
              <span>{t('home.clearChat', { defaultValue: '清空' })}</span>
            </button>
          )}
        </div>
      </div>

      {/* Chat Messages Stream */}
      <div className={`${s.chatStream} ${messages.length === 0 ? s.streamEmpty : ''}`} ref={chatStreamRef}>
        {messages.length === 0 ? (
          <div className={s.emptyState}>
            <div className={s.welcomeIconArea}>
              <Icon name="lightbulb" size={28} />
            </div>
            <h2 className={s.welcomeTitle}>
              {t('home.welcomeTitle', { defaultValue: '有什么关于项目与知识库的问题？' })}
            </h2>
            <p className={s.welcomeDesc}>
              {t('home.welcomeDesc', {
                defaultValue:
                  '我可以帮您检索各项目的架构说明、技术决策、代码笔记以及会话交接记录，并以项目和知识卡片形式为您呈现。',
              })}
            </p>

            <div className={s.quickPrompts}>
              {promptSuggestions.map((item, idx) => (
                <button
                  key={idx}
                  type="button"
                  className={s.promptCard}
                  onClick={() => void handleSend(item.text)}
                >
                  <Icon name={item.icon} size={15} className={s.promptIcon} />
                  <span className={s.promptText}>{item.text}</span>
                </button>
              ))}
            </div>
          </div>
        ) : (
          messages.map(msg => (
            <div
              key={msg.id}
              className={`${s.msgRow} ${msg.role === 'user' ? s.msgRowUser : ''}`}
            >
              <div
                className={`${s.msgAvatar} ${msg.role === 'user' ? s.avatarUser : s.avatarAi}`}
              >
                {/* "AI" rather than the brand mark: the mark belongs to the app
                    (navbar), not to every speaker in the transcript. Repeating
                    it here made one page carry the logo three times, and at
                    16px the card's hairline frame is sub-pixel anyway. Text
                    also mirrors the user's own "Me" badge. */}
                {msg.role === 'user' ? 'Me' : 'AI'}
              </div>

              <div className={s.msgBody}>
                <div className={s.msgMeta}>
                  <span>{msg.role === 'user' ? 'You' : 'Repotory AI'}</span>
                  <span>·</span>
                  <span>{msg.timestamp}</span>
                  {msg.scopeProjectName && (
                    <span className={s.scopeBadge}>
                      {t('home.scopeProject', { name: msg.scopeProjectName, defaultValue: msg.scopeProjectName })}
                    </span>
                  )}
                </div>

                {msg.role === 'user' ? (
                  <div className={s.bubbleUser}>{msg.content}</div>
                ) : (
                  <div className={s.bubbleAssistant}>
                    {/* Markdown Answer or typing indicator */}
                    {msg.content ? (
                      <div
                        className={s.markdownText}
                        dangerouslySetInnerHTML={{ __html: renderMarkdown(msg.content) }}
                      />
                    ) : (
                      <div className={s.typingIndicator}>
                        <span className={s.typingDot} />
                        <span className={s.typingDot} />
                        <span className={s.typingDot} />
                        {askElapsed > 0 && (
                          <span className={s.typingElapsed}>
                            {t('home.thinking')} · {askElapsed}s
                          </span>
                        )}
                      </div>
                    )}

                    {/* Related Project Cards */}
                    {msg.projectCards && msg.projectCards.length > 0 && (
                      <div className={s.cardSection}>
                        <div className={s.cardSectionHeader}>
                          <Icon name="grid" size={14} />
                          <span>
                            {t('home.relatedProjects', {
                              count: msg.projectCards.length,
                              defaultValue: `涉及项目卡片 (${msg.projectCards.length})`,
                            })}
                          </span>
                        </div>
                        <div className={s.projectCardsGrid}>
                          {msg.projectCards.map(p => (
                            <div
                              key={p.id}
                              className={s.projectCard}
                              onClick={() => navigate(`/project/${p.id}`)}
                              role="button"
                              tabIndex={0}
                              onKeyDown={e => {
                                if (e.key === 'Enter') navigate(`/project/${p.id}`)
                              }}
                            >
                              <div className={s.projectCardTop}>
                                <span className={s.projectName}>
                                  <Icon name="file-text" size={14} />
                                  {p.name}
                                </span>
                                {p.repo_count > 0 && (
                                  <span className={s.projectRepoBadge}>
                                    {p.repo_count} repos
                                  </span>
                                )}
                              </div>
                              <div className={s.projectPath} title={p.root_path}>
                                {p.root_path}
                              </div>
                              <div className={s.projectCardBottom}>
                                <span className={s.projectActivity}>
                                  {p.my_added > 0 ? `+${p.my_added} lines` : 'active'}
                                </span>
                                <span className={s.cardActionLink}>
                                  {t('home.viewProjectDetail', { defaultValue: '进入项目详情' })} →
                                </span>
                              </div>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}

                    {/* Cited Knowledge Cards */}
                    {msg.knowledgeCards && msg.knowledgeCards.length > 0 && (
                      <div className={s.cardSection}>
                        <div className={s.cardSectionHeader}>
                          <Icon name="file-text" size={14} />
                          <span>
                            {t('home.citedKnowledge', {
                              count: msg.knowledgeCards.length,
                              defaultValue: `引用知识卡片 (${msg.knowledgeCards.length})`,
                            })}
                          </span>
                        </div>
                        <div className={s.knowledgeCardsGrid}>
                          {msg.knowledgeCards.map(item => (
                            <div
                              key={`${item.ref || item.id}-${item.id}`}
                              className={s.knowledgeCard}
                              onClick={() => handleOpenKnowledge(item)}
                              role="button"
                              tabIndex={0}
                              onKeyDown={e => {
                                if (e.key === 'Enter') handleOpenKnowledge(item)
                              }}
                            >
                              <div className={s.knowledgeCardHead}>
                                {item.ref && <span className={s.refBadge}>[{item.ref}]</span>}
                                <span className={s.kindBadge}>
                                  {item.type === 'page'
                                    ? item.kind || 'Wiki'
                                    : item.type === 'todo'
                                      ? t('search:groupTodos', { defaultValue: '待办' })
                                      : t('dashboard.noteType', { defaultValue: '知识笔记' })}
                                </span>
                                {item.source_path && (
                                  <span className={s.sourceBadge} title={t('home.recallSource', { source: item.source_path })}>
                                    {item.source_path === 'graph'
                                      ? t('home.sourceGraph')
                                      : item.source_path === 'semantic'
                                      ? t('home.sourceSemantic')
                                      : item.source_path === 'fact'
                                      ? t('home.sourceFact')
                                      : t('home.sourceFts')}
                                  </span>
                                )}
                                {item.project_name && (
                                  <span className={s.projectBadge} title={item.project_name}>
                                    📁 {item.project_name}
                                  </span>
                                )}
                              </div>
                              <div className={s.knowledgeTitle} title={item.title}>
                                {item.title}
                              </div>
                              {item.snippet && (
                                <div className={s.knowledgeSnippet} title={item.snippet}>
                                  {item.snippet}
                                </div>
                              )}
                              <div className={s.cardActionLink}>
                                {t('home.viewKnowledgeDetail', { defaultValue: '查看知识详情' })} →
                              </div>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}

                    {/* Reply Actions */}
                    {!msg.isStreaming && msg.content && (
                      <div className={s.msgActions}>
                        <button
                          type="button"
                          className={s.actionBtn}
                          onClick={() => void handleCopy(msg.content)}
                        >
                          <Icon name="check" size={12} />
                          <span>{t('home.copyReply', { defaultValue: '复制' })}</span>
                        </button>
                        <button
                          type="button"
                          className={s.actionBtn}
                          onClick={() => void handleSaveAsNote(msg)}
                        >
                          <Icon name="plus" size={12} />
                          <span>{t('home.saveAsNote', { defaultValue: '存为笔记' })}</span>
                        </button>
                        {msg.evidence && (msg.evidence.items || []).length > 0 && (
                          <button
                            type="button"
                            className={s.actionBtn}
                            onClick={() => void handleFileAsPage(msg)}
                          >
                            <Icon name="file-text" size={12} />
                            <span>{t('home.saveAsPage', { defaultValue: '存为页面' })}</span>
                          </button>
                        )}
                      </div>
                    )}
                  </div>
                )}
              </div>
            </div>
          ))
        )}
      </div>

      {/* Input Bar */}
      <div className={s.inputArea}>
        <div className={s.inputBox}>
          <textarea
            ref={textareaRef}
            className={s.textarea}
            rows={2}
            value={question}
            onChange={e => setQuestion(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={t('home.inputPlaceholder', {
              defaultValue: '输入关于知识库或项目的问题… (Enter 发送，Shift+Enter 换行)',
            })}
            aria-label={t('home.inputPlaceholder', { defaultValue: '输入关于知识库或项目的问题' })}
          />
          {asking ? (
            <button
              type="button"
              className={s.sendBtn}
              onClick={handleStop}
              title={t('home.stop', { defaultValue: '停止' })}
            >
              <Icon name="close" size={16} />
            </button>
          ) : (
            <button
              type="button"
              className={s.sendBtn}
              onClick={() => void handleSend()}
              disabled={!question.trim()}
              title={t('home.send', { defaultValue: '发送' })}
            >
              <Icon name="arrow-up" size={16} />
            </button>
          )}
        </div>

        <div className={s.inputFooter}>
          {configured === false ? (
            <div className={s.fallbackNotice}>
              <Icon name="warning" size={13} />
              <span>
                {t('home.fallbackNotice', { defaultValue: '未配置 AI 端点，当前使用本地索引检索。' })}{' '}
                <button type="button" className={s.settingsLink} onClick={onGoToSettings}>
                  {t('home.goToSettings', { defaultValue: '前往设置' })}
                </button>
              </span>
            </div>
          ) : (
            <span>{t('home.assistantSub', { defaultValue: '与本地知识库、项目架构及会话记录实时互动' })}</span>
          )}
          <span>Enter ↵</span>
        </div>
      </div>
    </div>
  )
}
