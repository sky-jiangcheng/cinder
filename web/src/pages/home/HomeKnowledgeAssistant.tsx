import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useNavigationType, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import DOMPurify from 'dompurify'
import Icon from '../../components/Icon'
import {
  askAIWithEvidenceStream,
  searchAll,
  createNoteWithMeta,
  fileAnswerAsPage,
  getConfig,
  listAllTags,
  type Project,
  type Evidence,
  type EvidenceItem,
  type SearchHit,
} from '../../api/client'
import { renderMarkdown } from '../../utils/markdown'
import type { PushToast } from '../../hooks/useToast'
import s from './HomeKnowledgeAssistant.module.css'

// 首页对话台（ADR-0038 决策 2、3）。
//
// 这一版把首页从「四块性质不同的东西各做一半的杂烩」改成一台整页对话台：
//
//   - 空闲态：整页居中一个大输入框 + 三源合并的推荐区；
//   - 发起后：输入区常驻页面底部，对话流是页面**唯一**的滚动区。
//
// 布局由「有没有对话」决定，不是一个展开动画——旧版是一张 min-height 520px
// 的卡片，内部 .chatStream 还要自带 max-height: 580px 的滚动，嵌在会滚动的
// 页面里，于是问得越多越得滚两遍。
//
// 「问」和「搜」共用同一个输入框与一个**显式**模式开关（不靠「回车还是点按钮」
// 去猜）。两者唯一的耦合点是共享的项目作用域，外加「就这条追问」——把一条
// 检索命中接进对话流。

export type ConsoleMode = 'ask' | 'search'

/** 搜模式的一条命中分组：同一个项目下的笔记 / 待办。 */
export interface SearchGroup {
  projectId: number
  projectName: string
  hits: SearchHit[]
}

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
  /** 搜模式的结果：按项目分组。 */
  searchGroups?: SearchGroup[]
  /** 搜模式里按项目名命中的项目（searchAll 只搜笔记与待办，项目名匹配在客户端补）。 */
  projectMatches?: Project[]
  /** 这一条是检索结果，不是问答。 */
  kind?: 'ask' | 'search'
  /** 未配置 AI 端点时的本地回退——这条回复是关键词匹配，不是答案。 */
  localFallback?: boolean
}

interface Props {
  projects: Project[]
  onToast: PushToast
  onGoToSettings: () => void
  /** 项目列表是否已就绪。深链带 ?q= 时要等项目到了再跑，否则按项目名命中的那批会漏。 */
  projectsReady?: boolean
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

/** 一条检索命中的落点：笔记直接定位到那篇笔记，待办落到待办页签。 */
function hitHref(hit: SearchHit): string {
  return hit.type === 'note'
    ? `/project/${hit.project_id}?tab=notes&noteId=${hit.id}`
    : `/project/${hit.project_id}?tab=todos`
}

/**
 * 把命中按项目分组。分组顺序按命中数降序，同数按项目名排。
 *
 * 项目名不能直接用 `hit.project_name`：后端检索的 join 对一部分笔记留的是
 * NULL，组标题就成了空白——空标题读起来像「没有分组」，而不是「组名丢了」。
 * 先用命中自带的名字，缺了从项目列表解析，再缺就退回 id（一个我们不认识的
 * 项目至少还看得见编号）。
 */
function groupHitsByProject(hits: SearchHit[], projects: Project[]): SearchGroup[] {
  const names = new Map<number, string>()
  for (const p of projects) names.set(p.id, p.name)
  const map = new Map<number, SearchGroup>()
  for (const hit of hits) {
    const group = map.get(hit.project_id)
    if (group) {
      group.hits.push(hit)
      continue
    }
    map.set(hit.project_id, {
      projectId: hit.project_id,
      projectName: hit.project_name || names.get(hit.project_id) || `#${hit.project_id}`,
      hits: [hit],
    })
  }
  return [...map.values()].sort(
    (a, b) => b.hits.length - a.hits.length || a.projectName.localeCompare(b.projectName),
  )
}

// --- 输入区 -------------------------------------------------------------------

interface ConsoleInputProps {
  variant: 'hero' | 'docked'
  value: string
  placeholder: string
  busy: boolean
  stoppable: boolean
  textareaRef: React.RefObject<HTMLTextAreaElement | null>
  onChange: (value: string) => void
  onSubmit: () => void
  onStop: () => void
  footer: React.ReactNode
}

/**
 * 同一个输入框的两种尺寸。空闲态是居中的大输入框，发起后是常驻底部的紧凑条。
 *
 * 它必须是模块级的组件而不是渲染期定义的闭包——后者每次渲染都是新函数，
 * React 会把它当成新类型卸载重挂，textarea 会在每敲一个字时丢一次焦点。
 */
function ConsoleInput({
  variant,
  value,
  placeholder,
  busy,
  stoppable,
  textareaRef,
  onChange,
  onSubmit,
  onStop,
  footer,
}: ConsoleInputProps) {
  const { t } = useTranslation()
  return (
    <div className={`${s.inputArea} ${variant === 'hero' ? s.inputHero : s.inputDocked}`}>
      <div className={s.inputBox}>
        <textarea
          ref={textareaRef}
          className={s.textarea}
          rows={variant === 'hero' ? 3 : 2}
          value={value}
          onChange={e => onChange(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault()
              onSubmit()
            }
          }}
          placeholder={placeholder}
          aria-label={placeholder}
        />
        {busy && stoppable ? (
          <button
            type="button"
            className={s.sendBtn}
            onClick={onStop}
            title={t('home.stop', { defaultValue: '停止' })}
            aria-label={t('home.stop', { defaultValue: '停止' })}
          >
            <Icon name="close" size={16} />
          </button>
        ) : (
          <button
            type="button"
            className={s.sendBtn}
            onClick={onSubmit}
            disabled={!value.trim() || busy}
            title={t('home.send', { defaultValue: '发送' })}
            aria-label={t('home.send', { defaultValue: '发送' })}
          >
            <Icon name="arrow-up" size={16} />
          </button>
        )}
      </div>
      <div className={s.inputFooter}>{footer}</div>
    </div>
  )
}

// --- 主组件 -------------------------------------------------------------------

export default function HomeKnowledgeAssistant({
  projects,
  onToast,
  onGoToSettings,
  projectsReady = true,
}: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()

  const [mode, setMode] = useState<ConsoleMode>('ask')
  const [scopeProjectId, setScopeProjectId] = useState<number>(0)
  const [input, setInput] = useState('')
  const [asking, setAsking] = useState(false)
  const [searching, setSearching] = useState(false)
  const [configured, setConfigured] = useState<boolean | null>(null)
  const [messages, setMessages] = useState<DialogueMessage[]>([])
  const [tags, setTags] = useState<string[]>([])

  const streamRef = useRef<HTMLDivElement>(null)
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

  // The idle stage's hot tags. One request, shared with the recommendation row.
  useEffect(() => {
    let cancelled = false
    listAllTags()
      .then(tg => { if (!cancelled) setTags(tg ?? []) })
      .catch(() => { if (!cancelled) setTags([]) })
    return () => { cancelled = true }
  }, [])

  // Auto-scroll the stream as messages change or stream.
  useEffect(() => {
    if (streamRef.current && messages.length > 0) {
      streamRef.current.scrollTop = streamRef.current.scrollHeight
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

  // --- URL：?q= 是搜模式的查询词 ---------------------------------------------
  //
  // 深链契约：`/search?q=x` 重定向到 `/?q=x`，这里接管它并直接跑一次检索。
  //
  // **谁算「外部改了 URL」**：只有历史导航（后退 / 前进）。自己的写入走下面
  // 的提交路径。这不是洁癖——react-router 把 `setSearchParams` 当作一次
  // transition 应用，中间有一帧 URL 仍持有**旧值**；把那一帧的旧值当成外部
  // 改动，就会把用户刚提交的词又填回输入框（提交后框本该清空）。
  const urlQuery = searchParams.get('q') ?? ''
  const written = useRef<string | null>(null)
  const navType = useNavigationType()

  // The query the console should search for right now. Three things set it:
  // the deep link on mount, a history navigation, and an explicit submit.
  const [queryToRun, setQueryToRun] = useState<string | null>(null)
  // Every explicit submit bumps this. Re-running the same query leaves the
  // URL unchanged, so the query string alone cannot carry the "run it again".
  const [searchRequest, setSearchRequest] = useState(0)

  const writeQuery = useCallback(
    (q: string) => {
      written.current = q
      setSearchParams(q ? { q } : {}, { replace: true })
    },
    [setSearchParams],
  )

  const runSearch = useCallback(
    async (rawQuery: string) => {
      const q = rawQuery.trim()
      if (!q) return
      const targetProject = projects.find(p => p.id === scopeProjectId)
      const stamp = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
      const userId = `q-${Date.now()}`
      const resultId = `qr-${Date.now() + 1}`

      setMessages(prev => [
        ...prev,
        {
          id: userId,
          role: 'user',
          content: q,
          timestamp: stamp,
          scopeProjectId,
          scopeProjectName: targetProject ? targetProject.name : undefined,
        },
        {
          id: resultId,
          role: 'assistant',
          content: '',
          timestamp: stamp,
          isStreaming: true,
          scopeProjectId,
          kind: 'search',
        },
      ])
      setSearching(true)

      try {
        const hits = (await searchAll(q)) ?? []
        const scoped = scopeProjectId === 0 ? hits : hits.filter(h => h.project_id === scopeProjectId)
        const groups = groupHitsByProject(scoped, projects)
        // searchAll 只搜笔记与待办；按项目**名字**命中的项目在客户端补上，
        // 与旧搜索页同一套口径。
        const needle = q.toLowerCase()
        const projectMatches =
          scopeProjectId === 0
            ? projects.filter(p => p.name.toLowerCase().includes(needle)).slice(0, 8)
            : []

        setMessages(prev =>
          prev.map(m =>
            m.id === resultId
              ? { ...m, isStreaming: false, content: '', searchGroups: groups, projectMatches }
              : m,
          ),
        )
      } catch (err) {
        const detail = err instanceof Error ? err.message : String(err)
        setMessages(prev =>
          prev.map(m =>
            m.id === resultId
              ? {
                  ...m,
                  isStreaming: false,
                  content: `${t('search:searchFailed', { defaultValue: '检索失败' })}: ${detail}`,
                }
              : m,
          ),
        )
      } finally {
        setSearching(false)
      }
    },
    [projects, scopeProjectId, t],
  )

  // Adopt a query that came from outside: the deep link on mount, or a
  // history navigation.
  useEffect(() => {
    if (written.current === null) {
      // First mount. The URL's ?q= is a deep-link entry.
      written.current = urlQuery
      if (urlQuery.trim()) {
        setMode('search')
        setInput(urlQuery)
        setQueryToRun(urlQuery.trim())
      }
      return
    }
    if (navType !== 'POP') return
    if (urlQuery === written.current) return
    written.current = urlQuery
    setMode('search')
    setInput(urlQuery)
    setQueryToRun(urlQuery.trim() || null)
  }, [urlQuery, navType])

  // The single place a search is run. Everything — a submit, a deep link, the
  // back button — ends up here, so a query can never be searched twice by two
  // different paths.
  useEffect(() => {
    if (!queryToRun) return
    // Wait for the project list: the "project name matched" hits need it.
    if (!projectsReady) return
    void runSearch(queryToRun)
  }, [queryToRun, searchRequest, projectsReady, runSearch])

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

  // Ask the knowledge base. Reuses the existing streaming contract.
  const handleAsk = async (overridePrompt?: string) => {
    const promptText = (overridePrompt ?? input).trim()
    if (!promptText || asking) return

    setInput('')
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
      kind: 'ask',
    }

    const assistantMsgId = `ai-${Date.now() + 1}`
    const initialAssistantMsg: DialogueMessage = {
      id: assistantMsgId,
      role: 'assistant',
      content: '',
      timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
      isStreaming: true,
      scopeProjectId,
      kind: 'ask',
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
                  localFallback: true,
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

  // --- 模式切换 ---------------------------------------------------------------

  const switchMode = (next: ConsoleMode) => {
    if (next === mode) return
    setMode(next)
    window.setTimeout(() => textareaRef.current?.focus(), 0)
  }

  // --- 提交 -------------------------------------------------------------------

  const handleSubmit = () => {
    const text = input.trim()
    if (!text) return
    if (mode === 'search') {
      // The URL is the deep-link surface for the search query; the effect
      // above is the only place that actually runs it. The box clears: the
      // query stays visible in the transcript, which is where a conversation
      // keeps what has been said.
      writeQuery(text)
      setInput('')
      setQueryToRun(text)
      setSearchRequest(n => n + 1)
      return
    }
    void handleAsk(text)
  }

  // 「就这条追问」：把一条命中接进对话流。后端没有「以某条命中为上下文」的
  // 参数，所以用命中标题预填输入框并切到问模式——用户可以改完再发。
  const handleAskAbout = (hit: SearchHit) => {
    setMode('ask')
    setInput(t('home.askAboutHitPrompt', { title: hit.title, defaultValue: `关于「${hit.title}」，请展开说明：` }))
    window.setTimeout(() => textareaRef.current?.focus(), 0)
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

  // 热门项目：排序用 7 天窗口的个人新增（单日 my_added 在没有个人提交的日子会
  // 全是 0，排序会悄悄退化成字母序），但**不显示数字**——项目数、仓库数、
  // 新增行数只有仪表盘一处权威（ADR-0038 决策 5）。
  const hotProjects = useMemo(() => {
    return [...projects]
      .sort((a, b) =>
        b.my_added_7d - a.my_added_7d ||
        Number(b.is_starred) - Number(a.is_starred) ||
        b.repo_count - a.repo_count ||
        a.name.localeCompare(b.name))
      .slice(0, 6)
  }, [projects])

  const hotTags = useMemo(() => tags.slice(0, 12), [tags])

  const idle = messages.length === 0
  const busy = asking || searching

  const inputFooter = (
    <>
      {mode === 'ask' && configured === false ? (
        <span className={s.fallbackNotice}>
          <Icon name="warning" size={13} />
          <span>
            {t('home.fallbackHint', { defaultValue: '未配置 AI 端点，当前使用本地索引检索。' })}{' '}
            <button type="button" className={s.settingsLink} onClick={onGoToSettings}>
              {t('home.goToSettings', { defaultValue: '前往设置' })}
            </button>
          </span>
        </span>
      ) : (
        <span>{t('home.consoleSub', { defaultValue: '问出带出处的答案，或直接搜出命中' })}</span>
      )}
      <span>Enter ↵</span>
    </>
  )

  return (
    <div className={`${s.console} ${idle ? s.consoleIdle : s.consoleActive}`}>
      {/* Status bar. It stays a bar — not a title block — because the page below
          it is the console itself, not a card that happens to contain one. */}
      <div className={s.consoleHeader}>
        <div className={s.headerLeft}>
          <span className={s.consoleTitle}>{t('home.consoleTitle', { defaultValue: '知识库对话台' })}</span>
          {configured !== null && (
            <span className={s.statusChip} data-state={configured ? 'ai' : 'local'}>
              {configured
                ? t('home.statusAi', { defaultValue: 'AI 已连接' })
                : t('home.statusLocal', { defaultValue: '本地检索' })}
            </span>
          )}
        </div>

        <div className={s.headerRight}>
          <div className={s.modeSwitch} role="group" aria-label={t('home.modeLabel', { defaultValue: '检索模式' })}>
            <button
              type="button"
              className={`${s.modeBtn} ${mode === 'ask' ? s.modeBtnActive : ''}`}
              aria-pressed={mode === 'ask'}
              onClick={() => switchMode('ask')}
              title={t('home.modeAskHint', { defaultValue: '带出处的答案' })}
            >
              {t('home.modeAsk', { defaultValue: '问' })}
            </button>
            <button
              type="button"
              className={`${s.modeBtn} ${mode === 'search' ? s.modeBtnActive : ''}`}
              aria-pressed={mode === 'search'}
              onClick={() => switchMode('search')}
              title={t('home.modeSearchHint', { defaultValue: '命中列表' })}
            >
              {t('home.modeSearch', { defaultValue: '搜' })}
            </button>
          </div>

          <select
            className={s.scopeSelect}
            value={scopeProjectId}
            onChange={e => setScopeProjectId(Number(e.target.value))}
            aria-label={t('home.scopeSelectLabel', { defaultValue: '检索范围' })}
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

      <div className={s.consoleBody}>
        {idle ? (
          <div className={s.idleStage}>
            <div className={s.idleIntro}>
              <div className={s.welcomeIconArea}>
                <Icon name="lightbulb" size={26} />
              </div>
              <h1 className={s.welcomeTitle}>
                {t('home.welcomeTitle', { defaultValue: '有什么关于项目与知识库的问题？' })}
              </h1>
              <p className={s.welcomeDesc}>
                {t('home.welcomeDesc', {
                  defaultValue:
                    '可以问出带出处的答案，也可以直接搜出命中列表。知识库、项目架构与会话交接记录都在检索范围内。',
                })}
              </p>
            </div>

            <ConsoleInput
              variant="hero"
              value={input}
              placeholder={
                mode === 'search'
                  ? t('home.searchPlaceholder', { defaultValue: '搜笔记、待办与项目…' })
                  : t('home.askPlaceholder', { defaultValue: '问点什么…（Enter 发送，Shift+Enter 换行）' })
              }
              busy={busy}
              stoppable={false}
              textareaRef={textareaRef}
              onChange={setInput}
              onSubmit={handleSubmit}
              onStop={handleStop}
              footer={inputFooter}
            />

            {/* Three sources, one place. Hot projects and hot tags used to live
                on the search page's idle state, the quick prompts on the chat
                card's empty state — three copies of "what to do next". */}
            <div className={s.recoArea}>
              <h2 className={s.recoTitle}>{t('home.recoTitle', { defaultValue: '从这些开始' })}</h2>
              <div className={s.recoGrid}>
                <section className={s.recoBlock}>
                  <h3 className={s.recoBlockTitle}>
                    {t('home.recoQuickPrompts', { defaultValue: '快捷提问' })}
                  </h3>
                  <div className={s.promptList}>
                    {promptSuggestions.map((item, idx) => (
                      <button
                        key={idx}
                        type="button"
                        className={s.promptCard}
                        onClick={() => {
                          switchMode('ask')
                          void handleAsk(item.text)
                        }}
                      >
                        <Icon name={item.icon} size={14} className={s.promptIcon} />
                        <span className={s.promptText}>{item.text}</span>
                      </button>
                    ))}
                  </div>
                </section>

                <section className={s.recoBlock}>
                  <h3 className={s.recoBlockTitle}>
                    {t('search:hotProjects', { defaultValue: '热门项目' })}
                  </h3>
                  <div className={s.chipList}>
                    {hotProjects.map(p => (
                      <button
                        key={p.id}
                        type="button"
                        className={s.chip}
                        onClick={() => navigate(`/project/${p.id}`)}
                      >
                        {p.is_starred && <Icon name="star" size={11} filled={true} />}
                        {p.name}
                      </button>
                    ))}
                    {hotProjects.length === 0 && (
                      <span className={s.recoEmpty}>
                        {t('search:noHotProjects', { defaultValue: '还没有项目数据——先在设置里配置扫描目录' })}
                      </span>
                    )}
                  </div>
                </section>

                <section className={s.recoBlock}>
                  <h3 className={s.recoBlockTitle}>
                    {t('search:hotTags', { defaultValue: '热门标签' })}
                  </h3>
                  <div className={s.chipList}>
                    {hotTags.map(tag => (
                      <button
                        key={tag}
                        type="button"
                        className={s.chip}
                        onClick={() => {
                          switchMode('search')
                          setInput(tag)
                          writeQuery(tag)
                          setQueryToRun(tag)
                          setSearchRequest(n => n + 1)
                        }}
                      >
                        #{tag}
                      </button>
                    ))}
                    {hotTags.length === 0 && (
                      <span className={s.recoEmpty}>
                        {t('search:noHotTags', { defaultValue: '笔记里还没有标签' })}
                      </span>
                    )}
                  </div>
                </section>
              </div>
            </div>
          </div>
        ) : (
          <>
            <div className={s.stream} ref={streamRef}>
              {messages.map(msg => (
                <div
                  key={msg.id}
                  className={`${s.msgRow} ${msg.role === 'user' ? s.msgRowUser : ''}`}
                >
                  <div
                    className={`${s.msgAvatar} ${
                      msg.role === 'user' ? s.avatarUser : msg.kind === 'search' ? s.avatarSearch : s.avatarAi
                    }`}
                  >
                    {/* "AI" rather than the brand mark: the mark belongs to the app
                        (navbar), not to every speaker in the transcript. Repeating
                        it here made one page carry the logo three times, and at
                        16px the card's hairline frame is sub-pixel anyway. Text
                        also mirrors the user's own "Me" badge. */}
                    {msg.role === 'user' ? 'Me' : msg.kind === 'search' ? <Icon name="search" size={14} /> : 'AI'}
                  </div>

                  <div className={s.msgBody}>
                    <div className={s.msgMeta}>
                      <span>
                        {msg.role === 'user'
                          ? 'You'
                          : msg.kind === 'search'
                            ? t('search:results', { defaultValue: '搜索结果' })
                            : 'Repotory AI'}
                      </span>
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
                    ) : msg.kind === 'search' ? (
                      <div className={s.bubbleAssistant}>
                        {msg.isStreaming ? (
                          <div className={s.typingIndicator}>
                            <span className={s.typingDot} />
                            <span className={s.typingDot} />
                            <span className={s.typingDot} />
                          </div>
                        ) : msg.content ? (
                          <div className={s.markdownText}>{msg.content}</div>
                        ) : (
                          <SearchResults
                            groups={msg.searchGroups ?? []}
                            projectMatches={msg.projectMatches ?? []}
                            onAskAbout={handleAskAbout}
                          />
                        )}
                      </div>
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

                        {msg.localFallback && (
                          <span className={s.localBadge}>
                            {t('home.localMatchNotice', { defaultValue: '这是关键词匹配，不是答案' })}
                          </span>
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
              ))}
            </div>

            <ConsoleInput
              variant="docked"
              value={input}
              placeholder={
                mode === 'search'
                  ? t('home.searchPlaceholder', { defaultValue: '搜笔记、待办与项目…' })
                  : t('home.askPlaceholder', { defaultValue: '问点什么…（Enter 发送，Shift+Enter 换行）' })
              }
              busy={busy}
              stoppable={asking}
              textareaRef={textareaRef}
              onChange={setInput}
              onSubmit={handleSubmit}
              onStop={handleStop}
              footer={inputFooter}
            />
          </>
        )}
      </div>
    </div>
  )
}

// --- 搜模式的结果 -------------------------------------------------------------

function SearchResults({
  groups,
  projectMatches,
  onAskAbout,
}: {
  groups: SearchGroup[]
  projectMatches: Project[]
  onAskAbout: (hit: SearchHit) => void
}) {
  const { t } = useTranslation()
  const total = groups.reduce((n, g) => n + g.hits.length, 0)

  if (total === 0 && projectMatches.length === 0) {
    return (
      <div className={s.searchEmpty}>
        <p>{t('search:noResults', { defaultValue: '没有匹配的结果' })}</p>
      </div>
    )
  }

  return (
    <div className={s.searchResults}>
      <div className={s.searchCount}>
        {t('home.hitCount', { count: total + projectMatches.length, defaultValue: `{{count}} 条命中` })}
        <span className={s.searchGroupHint}>{t('home.searchGroupHint', { defaultValue: '按项目分组' })}</span>
      </div>

      {projectMatches.length > 0 && (
        <section className={s.searchGroup}>
          <h3 className={s.searchGroupTitle}>
            {t('search:groupProjects', { defaultValue: '项目' })}
            <span className={s.searchGroupCount}>{projectMatches.length}</span>
          </h3>
          <ul className={s.hitList}>
            {projectMatches.map(p => (
              <li key={`p-${p.id}`}>
                <Link to={`/project/${p.id}`} className={s.hitRow}>
                  <span className={`${s.hitType} hit-type-project`}>
                    {t('commandPalette.projectMark', { defaultValue: '项目' })}
                  </span>
                  <span className={s.hitBody}>
                    <span className={s.hitTitle}>{p.name}</span>
                    <span className={s.hitSnippet}>{p.root_path}</span>
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}

      {groups.map(g => (
        <section key={g.projectId} className={s.searchGroup}>
          <h3 className={s.searchGroupTitle}>
            {g.projectName}
            <span className={s.searchGroupCount}>{g.hits.length}</span>
          </h3>
          <ul className={s.hitList}>
            {g.hits.map(hit => (
              <li key={`${hit.type}-${hit.id}`} className={s.hitItem}>
                <Link to={hitHref(hit)} className={s.hitRow}>
                  <span className={`${s.hitType} hit-type-${hit.type}`}>
                    {hit.type === 'note'
                      ? t('commandPalette.noteMark', { defaultValue: '笔记' })
                      : t('commandPalette.todoMark', { defaultValue: '待办' })}
                  </span>
                  <span className={s.hitBody}>
                    <span className={s.hitTitle}>{hit.title}</span>
                    <span
                      className={s.hitSnippet}
                      dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(hit.snippet.slice(0, 120)) }}
                    />
                  </span>
                </Link>
                {/* The one coupling point between 搜 and 问: a hit can become the
                    next question. The backend has no "answer about this hit"
                    parameter, so the title is prefilled into the box instead. */}
                <button type="button" className={s.hitAsk} onClick={() => onAskAbout(hit)}>
                  {t('home.askAboutHit', { defaultValue: '就这条追问' })}
                </button>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  )
}
