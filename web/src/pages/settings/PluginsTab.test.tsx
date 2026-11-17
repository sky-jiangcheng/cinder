// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

// Partial mock: override the backend-calling endpoints, keep the rest real.
vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    getKnowledgeSources: vi.fn().mockResolvedValue([]),
    getKnowledgeSyncStatus: vi.fn().mockResolvedValue({ last_run: '', last_error: '' }),
    updateConfig: vi.fn().mockResolvedValue(undefined),
    triggerKnowledgeImport: vi.fn(),
    previewKnowledgeImport: vi.fn(),
    captureClaudeHandoff: vi.fn(),
  }
})

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

import { getKnowledgeSources, updateConfig } from '../../api/client'
import PluginsTab from './PluginsTab'

// The mount effect fetches sources and sync status. Waiting on the fetch keeps
// its state update inside waitFor's act scope, so the assertion below is not
// racing a promise that resolves after the test finished.
async function renderTab(config: Record<string, string>) {
  render(
    <PluginsTab
      initialAutoImport={config.auto_import !== '0'}
      initialClaudeCapture={config.claude_session_capture === '1'}
      initialTargets={{}}
      initialConfig={config}
      showMessage={() => {}}
    />,
  )
  await waitFor(() => expect(getKnowledgeSources).toHaveBeenCalled())
}

const agentToggle = () => screen.getByRole('button', { name: 'settings.agentInstLabel' })

beforeEach(() => {
  vi.clearAllMocks()
})

describe('PluginsTab agent instruction switch', () => {
  // The backend treats an unset key as ON, so the switch has to read the value
  // as "not explicitly off". Seeding it from `=== '1'` would show OFF while the
  // scan keeps writing the block — a switch that lies about the state.
  it('reads an unset key as on', async () => {
    await renderTab({})
    expect(agentToggle().getAttribute('aria-pressed')).toBe('true')
  })

  it('reads an explicit 0 as off', async () => {
    await renderTab({ agent_instructions_auto: '0' })
    expect(agentToggle().getAttribute('aria-pressed')).toBe('false')
  })

  it('persists the off switch through updateConfig', async () => {
    await renderTab({})
    fireEvent.click(agentToggle())
    await waitFor(() => expect(updateConfig).toHaveBeenCalledWith('agent_instructions_auto', '0'))
    expect(agentToggle().getAttribute('aria-pressed')).toBe('false')
  })

  it('persists the on switch through updateConfig', async () => {
    await renderTab({ agent_instructions_auto: '0' })
    fireEvent.click(agentToggle())
    await waitFor(() => expect(updateConfig).toHaveBeenCalledWith('agent_instructions_auto', '1'))
    expect(agentToggle().getAttribute('aria-pressed')).toBe('true')
  })
})
