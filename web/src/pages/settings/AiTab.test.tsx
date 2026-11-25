// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'

// Partial mock: override the backend-calling endpoints, keep the rest real.
vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    listAIModels: vi.fn(),
    listAIModelsConfigured: vi.fn(),
    updateConfig: vi.fn(),
    rebuildEmbeddings: vi.fn(),
    testAIChat: vi.fn(),
    testAIChatConfigured: vi.fn(),
  }
})

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: { defaultValue?: string }) =>
      opts?.defaultValue ?? key,
  }),
}))

import {
  listAIModels,
  listAIModelsConfigured,
  testAIChat,
  testAIChatConfigured,
  updateConfig,
} from '../../api/client'
import AiTab from './AiTab'

const baseConfig: Record<string, string> = {
  ai_chat_base_url: '',
  ai_chat_model: '',
  ai_chat_api_key: '',
}

function renderTab(config: Record<string, string>) {
  return render(
    <AiTab
      config={config}
      showMessage={() => {}}
      onSaved={() => {}}
    />,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('AiTab model pull', () => {
  it('does not show a model picker until a fetch has populated it', () => {
    renderTab(baseConfig)
    // 拉取前 models 为空 → 没有 ▾ 箭头，也没有浮层。
    expect(screen.queryByRole('button', { name: 'settings.ai.modelPicker' })).toBeNull()
  })

  it('auto-selects the first fetched model and shows a visible dropdown', async () => {
    vi.mocked(listAIModels).mockResolvedValue({
      models: ['model-a', 'model-b'], // 后端已按字典序排序
      resolved_base_url: '',
    })
    renderTab({ ...baseConfig, ai_chat_base_url: 'http://localhost:1234/v1' })

    fireEvent.click(screen.getByRole('button', { name: 'settings.ai.fetchModels' }))
    // 可见选择 UI 必须出现（WKWebView datalist 不渲染的回归守卫）：
    // combobox = 同一个输入框 + ▾ 箭头；auto-select 落在输入框。
    // 先等箭头出现——combobox 替换掉裸输入框后，旧节点引用不能再拿。
    await screen.findByRole('button', { name: 'settings.ai.modelPicker' })
    const input = screen.getByLabelText('settings.ai.model') as HTMLInputElement
    expect(input.value).toBe('model-a')
    const toggle = screen.getByRole('button', { name: 'settings.ai.modelPicker' })
    // 点箭头 → 浮层列出全部候选（打开时不过滤，否则已选名字把其余项滤光）
    fireEvent.click(toggle)
    const listbox = screen.getByRole('listbox', { name: 'settings.ai.modelPicker' })
    expect(listbox).toBeTruthy()
    // within：页面其他区块（语义检索/审核）也有 select 的 option，不能混入
    expect(within(listbox).getAllByRole('option').length).toBe(2)
  })

  it('lets the user pick a model from the visible dropdown', async () => {
    vi.mocked(listAIModels).mockResolvedValue({
      models: ['model-a', 'model-b'],
      resolved_base_url: '',
    })
    renderTab({ ...baseConfig, ai_chat_base_url: 'http://localhost:1234/v1' })

    fireEvent.click(screen.getByRole('button', { name: 'settings.ai.fetchModels' }))
    await screen.findByRole('button', { name: 'settings.ai.modelPicker' })
    fireEvent.click(screen.getByRole('button', { name: 'settings.ai.modelPicker' }))
    // 从浮层选 model-b → 回填输入框，浮层收起。
    fireEvent.click(screen.getByRole('option', { name: 'model-b' }))
    await waitFor(() =>
      expect((screen.getByLabelText('settings.ai.model') as HTMLInputElement).value).toBe('model-b'),
    )
    expect(screen.queryByRole('listbox')).toBeNull()
  })

  it('filters the reported models as you type inside the combobox', async () => {
    vi.mocked(listAIModels).mockResolvedValue({
      models: ['model-a', 'model-b', 'other-model'],
      resolved_base_url: '',
    })
    renderTab({ ...baseConfig, ai_chat_base_url: 'http://localhost:1234/v1' })

    fireEvent.click(screen.getByRole('button', { name: 'settings.ai.fetchModels' }))
    await screen.findByRole('button', { name: 'settings.ai.modelPicker' })
    // 在合并后的输入框里打字 → 浮层按字符过滤
    fireEvent.change(screen.getByLabelText('settings.ai.model'), { target: { value: 'model-b' } })
    await waitFor(() => {
      const listbox = screen.getByRole('listbox', { name: 'settings.ai.modelPicker' })
      expect(within(listbox).getAllByRole('option').map(o => o.textContent)).toEqual(['model-b'])
    })
  })

  it('hides the model picker after a successful save — the model is settled', async () => {
    vi.mocked(listAIModels).mockResolvedValue({
      models: ['model-a', 'model-b'],
      resolved_base_url: '',
    })
    vi.mocked(updateConfig).mockResolvedValue({ success: true })
    renderTab({ ...baseConfig, ai_chat_base_url: 'http://localhost:1234/v1' })

    // 拉取 → ▾ 箭头出现（供给选择）
    fireEvent.click(screen.getByRole('button', { name: 'settings.ai.fetchModels' }))
    await screen.findByRole('button', { name: 'settings.ai.modelPicker' })

    // 保存 → combobox 整体收起（箭头与浮层都消失），界面回到清爽的两框。
    // Save 按钮文案走 defaultValue（'Save'），且语义检索区有第二个同名按钮，
    // AI 区的排在前面。
    fireEvent.click(screen.getAllByRole('button', { name: 'Save' })[0])
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'settings.ai.modelPicker' })).toBeNull(),
    )
  })
})

describe('AiTab saved-key probes', () => {
  const savedConfig: Record<string, string> = {
    ai_chat_base_url: 'https://provider.example/v1',
    ai_chat_model: 'model-a',
    ai_chat_api_key: '********',
  }

  it('tests the saved provider without sending the masked key', async () => {
    vi.mocked(testAIChatConfigured).mockResolvedValue({ reply: 'OK', resolved_base_url: '' })
    renderTab(savedConfig)

    fireEvent.click(screen.getByRole('button', { name: 'settings.ai.testModel' }))

    await waitFor(() => expect(testAIChatConfigured).toHaveBeenCalledOnce())
    expect(testAIChat).not.toHaveBeenCalled()
  })

  it('fetches models from the saved provider without sending the masked key', async () => {
    vi.mocked(listAIModelsConfigured).mockResolvedValue({ models: ['model-a'], resolved_base_url: '' })
    renderTab(savedConfig)

    fireEvent.click(screen.getByRole('button', { name: 'settings.ai.fetchModels' }))

    await waitFor(() => expect(listAIModelsConfigured).toHaveBeenCalledOnce())
    expect(listAIModels).not.toHaveBeenCalled()
  })

  it('requires a fresh key before probing a changed endpoint', async () => {
    renderTab(savedConfig)
    fireEvent.change(screen.getByLabelText('settings.ai.baseUrl'), {
      target: { value: 'https://other.example/v1' },
    })

    fireEvent.click(screen.getByRole('button', { name: 'settings.ai.fetchModels' }))

    expect(screen.getByText('settings.ai.reenterApiKey')).toBeTruthy()
    expect(listAIModelsConfigured).not.toHaveBeenCalled()
    expect(listAIModels).not.toHaveBeenCalled()
  })

  it('requires a fresh key before testing a changed model', async () => {
    renderTab(savedConfig)
    fireEvent.change(screen.getByLabelText('settings.ai.model'), {
      target: { value: 'model-b' },
    })

    fireEvent.click(screen.getByRole('button', { name: 'settings.ai.testModel' }))

    expect(screen.getByText('settings.ai.reenterApiKey')).toBeTruthy()
    expect(testAIChatConfigured).not.toHaveBeenCalled()
    expect(testAIChat).not.toHaveBeenCalled()
  })

  it('uses the typed credentials when the user enters a fresh key', async () => {
    vi.mocked(testAIChat).mockResolvedValue({ reply: 'OK', resolved_base_url: '' })
    renderTab(savedConfig)
    fireEvent.change(screen.getByLabelText('settings.ai.apiKeyOptional'), {
      target: { value: 'fresh-key' },
    })

    fireEvent.click(screen.getByRole('button', { name: 'settings.ai.testModel' }))

    await waitFor(() => expect(testAIChat).toHaveBeenCalledWith(
      'https://provider.example/v1',
      'model-a',
      'fresh-key',
    ))
    expect(testAIChatConfigured).not.toHaveBeenCalled()
  })
})
