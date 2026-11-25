import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  listAIModels,
  listAIModelsConfigured,
  rebuildEmbeddings,
  testAIChat,
  testAIChatConfigured,
  updateConfig,
} from '../../api/client'
import AiAuditSection from './AiAuditSection'
import WikiReviewSection from './WikiReviewSection'

const SECRET_MASK = '********'

// 预留钩子类：`ai-url-row` —— 目前全站没有对应样式定义（P35 复核确认），
// 保留在 markup 里作为将来挂样式的锚点，删留都不影响行为。

interface Props {
  /** Flat key/value config record (data.config from GetConfig). */
  config: Record<string, string>
  showMessage: (msg: string) => void
  onSaved: () => void
}

/**
 * AI chat endpoint configuration: any OpenAI-compatible /chat/completions
 * server works — a local LM Studio (http://localhost:1234/v1), vLLM, Ollama,
 * or a remote provider. Base URL can be probed (model list), the model picked
 * from what the server reports, and the model itself tested with a tiny
 * completion — all against the typed values, no save required. The API key
 * is optional (local servers usually need none), stored server-side, and
 * redacted on read.
 */
export default function AiTab({ config, showMessage, onSaved }: Props) {
  const { t } = useTranslation()
  const [baseURL, setBaseURL] = useState(config.ai_chat_base_url || '')
  const [model, setModel] = useState(config.ai_chat_model || '')
  const [apiKey, setApiKey] = useState(config.ai_chat_api_key || '')
  const [saving, setSaving] = useState(false)
  const [models, setModels] = useState<string[]>([])
  const [modelsLoading, setModelsLoading] = useState(false)
  const [modelsMsg, setModelsMsg] = useState('')
  const [testMsg, setTestMsg] = useState('')
  const [testing, setTesting] = useState(false)
  const [semanticEnabled, setSemanticEnabled] = useState(config.semantic_search === '1')
  const [embeddingBaseURL, setEmbeddingBaseURL] = useState(config.embedding_base_url || '')
  const [embeddingModel, setEmbeddingModel] = useState(config.embedding_model || '')
  const [embeddingAPIKey, setEmbeddingAPIKey] = useState(config.embedding_api_key || '')
  const [vectorStore, setVectorStore] = useState(config.vector_store || 'local')
  const [vectorStoreURL, setVectorStoreURL] = useState(config.vector_store_url || '')
  const [vectorStoreAPIKey, setVectorStoreAPIKey] = useState(config.vector_store_api_key || '')
  const [vectorCollection, setVectorCollection] = useState(config.vector_store_collection || '')
  const [semanticSaving, setSemanticSaving] = useState(false)
  const [rebuilding, setRebuilding] = useState(false)
  const [semanticMsg, setSemanticMsg] = useState('')
  // 保存成功的按钮级反馈：全局 toast 之外，按钮本身也要"动一下"——之前保存
  // 成功只有右上角一条消息扫过，表单和按钮毫无变化，用户不知道存没存上。
  const [savedFlash, setSavedFlash] = useState(false)
  // 模型下拉跟「拉取→保存」周期走：拉取成功才出现（供选择），保存成功即消失
  // （模型已定，界面回归清爽）。改了地址/模型后重新拉取，下拉会再回来。
  const [pickerVisible, setPickerVisible] = useState(false)
  // combobox：一个控件同时承担手动输入与列表选择。models 就绪后输入框右侧
  // 出现 ▾ 箭头，点开是过滤式浮层（按已输入的字符过滤，WKWebView 的原生
  // <datalist> 不渲染下拉，所以自绘）；选择回填输入框，Escape / 点外部关闭。
  const [pickerOpen, setPickerOpen] = useState(false)
  // 过滤基准：点箭头打开时为空（显示全部），在输入框里打字才按字符过滤——
  // 否则 auto-select 填入的模型名会把其余候选全部滤掉，改选无从谈起。
  const [pickerQuery, setPickerQuery] = useState('')
  const comboRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!pickerOpen) return
    const onDocMouseDown = (e: MouseEvent) => {
      if (comboRef.current && !comboRef.current.contains(e.target as Node)) setPickerOpen(false)
    }
    document.addEventListener('mousedown', onDocMouseDown)
    return () => document.removeEventListener('mousedown', onDocMouseDown)
  }, [pickerOpen])
  const filteredModels = models.filter(m =>
    m.toLowerCase().includes(pickerQuery.trim().toLowerCase()),
  )

  const savedBaseURL = config.ai_chat_base_url || ''
  const savedModel = config.ai_chat_model || ''
  const keyIsMasked = apiKey.trim() === SECRET_MASK
  const canUseSavedCredentials = (nextBaseURL: string, nextModel?: string) =>
    config.ai_chat_api_key === SECRET_MASK && keyIsMasked &&
    nextBaseURL.trim() === savedBaseURL.trim() && !!savedModel.trim() &&
    (nextModel === undefined || nextModel.trim() === savedModel.trim())

  const fetchModels = async () => {
    if (!baseURL.trim() || modelsLoading) return
    if (keyIsMasked && !canUseSavedCredentials(baseURL)) {
      setModelsMsg(t('settings.ai.reenterApiKey'))
      return
    }
    setModelsLoading(true)
    setModelsMsg('')
    setModels([])
    try {
      const res = canUseSavedCredentials(baseURL)
        ? await listAIModelsConfigured()
        : await listAIModels(baseURL.trim(), apiKey.trim())
      setModels(res.models)
      // 拉取成功 → 下拉出现（供给选择）。Auto-select the first fetched model
      // when the field is empty so the pull has a visible result. Previously
      // the list only fed a <datalist>, which WKWebView renders weakly, so
      // after "拉取模型" the picker looked unchanged.
      if (res.models.length > 0) setPickerVisible(true)
      if (res.models.length > 0 && model.trim() === '') {
        setModel(res.models[0])
      }
      // The probe tries near-miss path variants (LM Studio's base is /v1 but
      // ".../api/v1" is the classic mistype); adopt the URL that worked.
      if (res.resolved_base_url && res.resolved_base_url !== baseURL.trim()) {
        setBaseURL(res.resolved_base_url)
        setModelsMsg(t('settings.ai.modelsFoundResolved', { count: res.models.length, url: res.resolved_base_url }))
      } else {
        setModelsMsg(t('settings.ai.modelsFound', { count: res.models.length }))
      }
    } catch (e: unknown) {
      setModelsMsg(t('settings.ai.fetchFail', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setModelsLoading(false)
    }
  }

  const testModel = async () => {
    if (!model.trim() || testing) return
    if (keyIsMasked && !canUseSavedCredentials(baseURL, model)) {
      setTestMsg(t('settings.ai.reenterApiKey'))
      return
    }
    setTesting(true)
    setTestMsg('')
    try {
      const res = canUseSavedCredentials(baseURL, model)
        ? await testAIChatConfigured()
        : await testAIChat(baseURL.trim(), model.trim(), apiKey.trim())
      if (res.resolved_base_url && res.resolved_base_url !== baseURL.trim()) {
        setBaseURL(res.resolved_base_url)
      }
      setTestMsg(t('settings.ai.testOk', { reply: res.reply.slice(0, 80) }))
    } catch (e: unknown) {
      setTestMsg(t('settings.ai.testFail', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setTesting(false)
    }
  }

  const save = async (): Promise<boolean> => {
    if (!baseURL.trim() || !model.trim()) {
      showMessage(t('settings.ai.missingFields'))
      return false
    }
    setSaving(true)
    try {
      await updateConfig('ai_chat_base_url', baseURL.trim())
      await updateConfig('ai_chat_model', model.trim())
      // The masked placeholder round-trips as "unchanged"; only overwrite the
      // secret when the user typed a fresh value.
      if (apiKey.trim() && apiKey !== SECRET_MASK) {
        await updateConfig('ai_chat_api_key', apiKey.trim())
      }
      showMessage(t('settings.configSaved'))
      onSaved()
      setSavedFlash(true)
      setTimeout(() => setSavedFlash(false), 2000)
      // 模型已定 → 下拉收起，界面回到清爽的两框（地址 + 模型）。
      setPickerVisible(false)
      return true
    } catch (e: unknown) {
      showMessage(t('settings.saveFailedMsg', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
      return false
    } finally {
      setSaving(false)
    }
  }

  const saveSemantic = async (nextEnabled: boolean): Promise<boolean> => {
    if (nextEnabled && (!embeddingBaseURL.trim() || !embeddingModel.trim())) {
      showMessage(t('settings.semantic.missingFields'))
      return false
    }
    setSemanticSaving(true)
    setSemanticMsg('')
    try {
      await updateConfig('semantic_search', nextEnabled ? '1' : '0')
      await updateConfig('embedding_base_url', embeddingBaseURL.trim())
      await updateConfig('embedding_model', embeddingModel.trim())
      // GetConfig masks both secrets. A placeholder means "keep the stored
      // value"; sending it back would replace the real key with stars.
      if (embeddingAPIKey.trim() && embeddingAPIKey !== SECRET_MASK) {
        await updateConfig('embedding_api_key', embeddingAPIKey.trim())
      }
      await updateConfig('vector_store', vectorStore)
      await updateConfig('vector_store_url', vectorStoreURL.trim())
      if (vectorStoreAPIKey.trim() && vectorStoreAPIKey !== SECRET_MASK) {
        await updateConfig('vector_store_api_key', vectorStoreAPIKey.trim())
      }
      await updateConfig('vector_store_collection', vectorCollection.trim())
      showMessage(t('settings.configSaved'))
      onSaved()
      return true
    } catch (e: unknown) {
      showMessage(t('settings.saveFailedMsg', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
      return false
    } finally {
      setSemanticSaving(false)
    }
  }

  const rebuild = async () => {
    setRebuilding(true)
    setSemanticMsg('')
    try {
      const count = await rebuildEmbeddings()
      setSemanticMsg(t('settings.semantic.rebuildDone', { count }))
      onSaved()
    } catch (e: unknown) {
      setSemanticMsg(t('settings.semantic.rebuildFailed', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setRebuilding(false)
    }
  }

  return (
    <div className="settings-section">
      <h2>{t('settings.ai.title')}</h2>
      <p className="section-desc">{t('settings.ai.desc')}</p>

      <div className="form-group">
        <label htmlFor="ai-base-url">{t('settings.ai.baseUrl')}</label>
        <div className="ai-url-row">
          <input
            id="ai-base-url"
            type="text"
            value={baseURL}
            onChange={(e) => setBaseURL(e.target.value)}
            className="form-input"
            placeholder="http://localhost:1234/v1"
          />
          <button
            className="btn btn-secondary"
            onClick={fetchModels}
            disabled={modelsLoading || !baseURL.trim()}
          >
            {modelsLoading ? t('settings.ai.fetching') : t('settings.ai.fetchModels')}
          </button>
        </div>
        <span className="form-hint">{t('settings.ai.baseUrlHint')}</span>
        {modelsMsg && <span className="form-hint">{modelsMsg}</span>}
      </div>

      <div className="form-group">
        <label htmlFor="ai-model">{t('settings.ai.model')}</label>
        {/* 拉取到的模型与输入框合并成一个 combobox：手动输入与列表选择共用
            一个控件，不再有两个框。WKWebView 的原生 <datalist> 不渲染下拉
            （历史教训），所以箭头与浮层自绘；浮层按已输入字符过滤，Escape /
            点击外部关闭。可见性跟「拉取→保存」周期走：拉取成功出现，保存
            成功消失——回归守卫钉住这条链。 */}
        {models.length > 0 && pickerVisible && (
          <div className="model-combo" ref={comboRef}>
            <input
              id="ai-model"
              type="text"
              value={model}
              onChange={(e) => {
                setModel(e.target.value)
                setPickerQuery(e.target.value)
                setPickerOpen(true)
              }}
              onKeyDown={(e) => {
                if (e.key === 'Escape') setPickerOpen(false)
              }}
              className="form-input"
              placeholder="qwen2.5-7b-instruct / gpt-4o-mini / …"
              autoComplete="off"
            />
            <button
              type="button"
              className="model-combo-toggle"
              aria-label={t('settings.ai.modelPicker', { count: models.length })}
              aria-expanded={pickerOpen}
              onClick={() => {
                setPickerQuery('')
                setPickerOpen(o => !o)
              }}
            >
              ▾
            </button>
            {pickerOpen && (
              <div
                className="model-combo-pop"
                role="listbox"
                aria-label={t('settings.ai.modelPicker', { count: models.length })}
              >
                {filteredModels.length === 0 && (
                  <div className="model-combo-empty">{t('settings.ai.modelNoMatch')}</div>
                )}
                {filteredModels.map(m => (
                  <button
                    key={m}
                    type="button"
                    role="option"
                    aria-selected={m === model.trim()}
                    className="model-combo-option"
                    onClick={() => {
                      setModel(m)
                      setPickerOpen(false)
                    }}
                  >
                    {m}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
        {!(models.length > 0 && pickerVisible) && (
          <input
            id="ai-model"
            type="text"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            className="form-input"
            placeholder="qwen2.5-7b-instruct / gpt-4o-mini / …"
          />
        )}
        <span className="form-hint">{t('settings.ai.modelHint')}</span>
      </div>

      <div className="form-group">
        <label htmlFor="ai-api-key">{t('settings.ai.apiKeyOptional')}</label>
        <input
          id="ai-api-key"
          type="password"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          className="form-input"
          placeholder={t('settings.ai.apiKeyPlaceholder')}
        />
        <span className="form-hint">{t('settings.ai.apiKeyHint')}</span>
        {keyIsMasked && <span className="form-hint">{t('settings.ai.maskedKeyHint')}</span>}
      </div>

      <div className="empty-actions">
        <button className="btn btn-primary" onClick={testModel} disabled={testing || !model.trim()}>
          {testing ? t('settings.ai.testing') : t('settings.ai.testModel')}
        </button>
        <button className="btn btn-secondary" onClick={save} disabled={saving || testing}>
          {savedFlash
            ? t('settings.configSaved')
            : saving
              ? t('settings.saving', { defaultValue: 'Saving…' })
              : t('settings.save', { defaultValue: 'Save' })}
        </button>
      </div>
      {testMsg && <p className="form-hint">{testMsg}</p>}

      <h3>{t('settings.semantic.title')}</h3>
      <p className="section-desc">{t('settings.semantic.desc')}</p>

      <div className="form-group">
        <label htmlFor="semantic-enabled">
          <input
            id="semantic-enabled"
            type="checkbox"
            checked={semanticEnabled}
            onChange={(e) => setSemanticEnabled(e.target.checked)}
          />
          {t('settings.semantic.enabled')}
        </label>
        <span className="form-hint">{t('settings.semantic.enabledHint')}</span>
      </div>

      <div className="form-group">
        <label htmlFor="embedding-base-url">{t('settings.semantic.baseUrl')}</label>
        <input
          id="embedding-base-url"
          type="text"
          value={embeddingBaseURL}
          onChange={(e) => setEmbeddingBaseURL(e.target.value)}
          className="form-input"
          placeholder="http://localhost:11434/v1"
        />
      </div>

      <div className="form-group">
        <label htmlFor="embedding-model">{t('settings.semantic.model')}</label>
        <input
          id="embedding-model"
          type="text"
          value={embeddingModel}
          onChange={(e) => setEmbeddingModel(e.target.value)}
          className="form-input"
          placeholder="nomic-embed-text / text-embedding-3-small"
        />
      </div>

      <div className="form-group">
        <label htmlFor="embedding-api-key">{t('settings.semantic.apiKeyOptional')}</label>
        <input
          id="embedding-api-key"
          type="password"
          value={embeddingAPIKey}
          onChange={(e) => setEmbeddingAPIKey(e.target.value)}
          className="form-input"
          placeholder={t('settings.ai.apiKeyPlaceholder')}
        />
      </div>

      <div className="form-group">
        <label htmlFor="vector-store">{t('settings.semantic.vectorStore')}</label>
        <select
          id="vector-store"
          value={vectorStore}
          onChange={(e) => setVectorStore(e.target.value)}
          className="form-input"
        >
          <option value="local">{t('settings.semantic.localStore')}</option>
          <option value="qdrant">Qdrant</option>
          <option value="weaviate">Weaviate</option>
          <option value="chromem">chromem</option>
        </select>
      </div>

      {vectorStore !== 'local' && (
        <>
          <div className="form-group">
            <label htmlFor="vector-store-url">{t('settings.semantic.vectorUrl')}</label>
            <input
              id="vector-store-url"
              type="text"
              value={vectorStoreURL}
              onChange={(e) => setVectorStoreURL(e.target.value)}
              className="form-input"
            />
          </div>
          <div className="form-group">
            <label htmlFor="vector-store-api-key">{t('settings.semantic.vectorApiKey')}</label>
            <input
              id="vector-store-api-key"
              type="password"
              value={vectorStoreAPIKey}
              onChange={(e) => setVectorStoreAPIKey(e.target.value)}
              className="form-input"
            />
          </div>
          <div className="form-group">
            <label htmlFor="vector-collection">{t('settings.semantic.collection')}</label>
            <input
              id="vector-collection"
              type="text"
              value={vectorCollection}
              onChange={(e) => setVectorCollection(e.target.value)}
              className="form-input"
            />
          </div>
        </>
      )}

      <div className="empty-actions">
        <button
          className="btn btn-primary"
          onClick={() => saveSemantic(semanticEnabled)}
          disabled={semanticSaving || rebuilding}
        >
          {semanticSaving ? t('settings.saving', { defaultValue: 'Saving…' }) : t('settings.save', { defaultValue: 'Save' })}
        </button>
        <button
          className="btn btn-secondary"
          onClick={rebuild}
          disabled={semanticSaving || rebuilding || !semanticEnabled || !embeddingBaseURL.trim() || !embeddingModel.trim()}
        >
          {rebuilding ? t('settings.semantic.rebuilding') : t('settings.semantic.rebuild')}
        </button>
      </div>
      {semanticMsg && <p className="form-hint">{semanticMsg}</p>}

      {/* AI call audit and content review (ADR-0030). It is a SEPARATE block
          below the semantic-search section rather than a fourth tab: this is
          an add-on capability, and putting it on its own tab would present it
          as a peer of "scan" or "appearance" — which is exactly the product
          change ADR-0030 refused to make. */}
      <WikiReviewSection config={config} showMessage={showMessage} />

      <AiAuditSection config={config} showMessage={showMessage} />
    </div>
  )
}
