// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'

// Partial mock: override the backend-calling endpoints, keep the rest real.
vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    triggerScan: vi.fn(),
    importClaudeMemory: vi.fn(),
    runBackupNow: vi.fn(),
    updateConfig: vi.fn(),
    checkBackupCopyTarget: vi.fn(),
    listAgentClients: vi.fn().mockResolvedValue([]),
    setupAgentClients: vi.fn(),
    getProjects: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? key,
  }),
}))

import { checkBackupCopyTarget, updateConfig, listAgentClients, setupAgentClients, getProjects } from '../../api/client'
import type { Project } from '../../api/types'
import { invalidateCache } from '../../hooks/useApiData'
import ActionsTab from './ActionsTab'

const baseConfig: Record<string, string> = {}

function renderTab(config: Record<string, string> = baseConfig) {
  // The tab renders a react-router <Link>, so it needs a router in scope.
  return render(
    <MemoryRouter>
      <ActionsTab initialConfig={config} showMessage={() => {}} />
    </MemoryRouter>,
  )
}

// The off-disk copy (ADR-0025 L3). The point of these tests is the two
// things the user cannot get from the path itself: that a verdict is shown
// in the user's language, and that a masked secret is never written back.
describe('ActionsTab off-disk copy', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('checks the directory the user typed', async () => {
    vi.mocked(checkBackupCopyTarget).mockResolvedValue({
      path: '/Volumes/Backup/repotory',
      exists: true,
      writable: true,
      same_device: false,
      device_known: true,
      code: '',
      blocking: false,
    })
    renderTab()

    fireEvent.change(screen.getByRole('textbox', { name: 'settings.backupCopyPath' }), {
      target: { value: '/Volumes/Backup/repotory' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'settings.backupCopyCheck' }))

    await waitFor(() => expect(checkBackupCopyTarget).toHaveBeenCalledWith('/Volumes/Backup/repotory'))
    // A usable target says so — in the user's language, not in the
    // backend's.
    expect(await screen.findByText('settings.backupCopyOk')).toBeTruthy()
  })

  it('shows the same-disk advice instead of a generic message', async () => {
    vi.mocked(checkBackupCopyTarget).mockResolvedValue({
      path: '/data/backups',
      exists: true,
      writable: true,
      same_device: true,
      device_known: true,
      code: 'same_device',
      blocking: false,
    })
    renderTab()

    fireEvent.click(screen.getByRole('button', { name: 'settings.backupCopyCheck' }))

    expect(await screen.findByText('settings.backupCopySameDevice')).toBeTruthy()
  })

  it('shows the blocking reason for a target that cannot work', async () => {
    vi.mocked(checkBackupCopyTarget).mockResolvedValue({
      path: 'backups/offdisk',
      exists: false,
      writable: false,
      same_device: false,
      device_known: true,
      code: 'relative_path',
      blocking: true,
    })
    renderTab()

    fireEvent.click(screen.getByRole('button', { name: 'settings.backupCopyCheck' }))

    expect(await screen.findByText('settings.backupCopyRelativePath')).toBeTruthy()
  })

  it('saves the directory through the config write path', async () => {
    vi.mocked(updateConfig).mockResolvedValue({ success: true })
    renderTab()

    fireEvent.change(screen.getByRole('textbox', { name: 'settings.backupCopyPath' }), {
      target: { value: '  /Volumes/Backup/repotory  ' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'settings.backupCopySave' }))

    await waitFor(() =>
      expect(updateConfig).toHaveBeenCalledWith('backup_copy_dir', '/Volumes/Backup/repotory'),
    )
  })
})

describe('ActionsTab remote secret', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('does not write a masked secret back over the real credential', async () => {
    vi.mocked(updateConfig).mockResolvedValue({ success: true })
    // The backend returns a configured secret masked; saving the form must
    // not replace the credential with the mask.
    renderTab({ backup_remote_enabled: '1', backup_remote_secret_key: '********' })

    fireEvent.click(screen.getByRole('button', { name: 'settings.backupRemoteSave' }))

    await waitFor(() => expect(updateConfig).toHaveBeenCalled())
    const written = vi.mocked(updateConfig).mock.calls.map((c) => c[0])
    expect(written).not.toContain('backup_remote_secret_key')
  })

  it('writes a secret the user actually typed', async () => {
    vi.mocked(updateConfig).mockResolvedValue({ success: true })
    renderTab({ backup_remote_enabled: '1', backup_remote_secret_key: '********' })

    const secretInput = screen.getByLabelText('settings.backupRemoteSecretKey')
    fireEvent.change(secretInput, { target: { value: 'new-secret' } })
    fireEvent.click(screen.getByRole('button', { name: 'settings.backupRemoteSave' }))

    await waitFor(() =>
      expect(updateConfig).toHaveBeenCalledWith('backup_remote_secret_key', 'new-secret'),
    )
  })
})

// The one-click AI client wiring. What matters here is that the button
// asks for the user's own choices (which project, which clients) and that a
// refusal from the backend reaches the user in their own language.
describe('ActionsTab AI client wiring', () => {
  const project = (over: Partial<Project>): Project => ({
    id: 1,
    name: 'demo',
    root_path: '/code/demo',
    level_override: 0,
    is_auto_grouped: false,
    is_starred: false,
    created_at: '2026-10-01',
    project_kind: 'scanned',
    repo_count: 1,
    total_added: 0,
    total_deleted: 0,
    my_added: 0,
    my_deleted: 0,
    my_files: 0,
    my_added_7d: 0,
    is_workday: true,
    below_standard: false,
    ...over,
  })

  beforeEach(() => {
    vi.clearAllMocks()
    // The project list is shared through the module-level cache in
    // useApiData, and an earlier test in this file mounted the tab with an
    // empty list. Drop it so this block sees its own projects.
    invalidateCache('projects:all')
    vi.mocked(listAgentClients).mockResolvedValue(['claude', 'cursor'])
    vi.mocked(getProjects).mockResolvedValue([
      project({ id: 1, name: 'demo' }),
      project({ id: 2, name: 'home-container', is_auto_grouped: true }),
      project({ id: 3, name: 'uncategorized', root_path: 'repotory://memory/default', project_kind: 'default' }),
    ])
    vi.mocked(setupAgentClients).mockResolvedValue({
      code: '',
      blocking: false,
      binary: '/usr/local/bin/repotory-mcp',
      files: [{ client: 'claude', path: '/code/demo/.mcp.json', status: 'created' }],
    })
  })

  it('offers the clients the backend supports, not a hardcoded list', async () => {
    renderTab()

    expect(await screen.findByRole('checkbox', { name: 'settings.agentClientClaude' })).toBeTruthy()
    expect(screen.getByRole('checkbox', { name: 'settings.agentClientCursor' })).toBeTruthy()
    expect(screen.queryByRole('checkbox', { name: 'settings.agentClientWindsurf' })).toBeNull()
  })

  it('offers only the projects that can receive client configuration', async () => {
    renderTab()

    expect(await screen.findByRole('option', { name: 'demo' })).toBeTruthy()
    // A scan-time container and the synthetic bucket are refused by the
    // backend, so offering them would be offering a dead end.
    expect(screen.queryByRole('option', { name: 'home-container' })).toBeNull()
    expect(screen.queryByRole('option', { name: 'uncategorized' })).toBeNull()
  })

  // Pick demo + Claude and press Connect. The project list arrives
  // asynchronously, and a <select> whose option is not there yet coerces the
  // value to '' — the change then lands as "no project picked" and the connect
  // handler early-returns, so waiting for the select alone is a race the test
  // can lose (it did, on CI). Wait for the option itself.
  const pickDemoAndConnect = async () => {
    const picker = await screen.findByRole('combobox')
    await screen.findByRole('option', { name: 'demo' })
    fireEvent.change(picker, { target: { value: '1' } })
    fireEvent.click(screen.getByRole('checkbox', { name: 'settings.agentClientClaude' }))
    fireEvent.click(screen.getByRole('button', { name: 'settings.agentClientsConnect' }))
  }

  it('connects the picked project and clients', async () => {
    renderTab()

    await pickDemoAndConnect()

    await waitFor(() =>
      expect(setupAgentClients).toHaveBeenCalledWith(1, ['claude'], true, true),
    )
  })

  // ADR-0043: the instruction block is a separate switch from the hook, because
  // the two cover different agents. Unticking either must change only its own
  // argument — a UI that sends the same flag for both would silently write into
  // the user's tracked AGENTS.md when they only asked for a Claude Code hook.
  it('sends the instruction switch independently of the hook switch', async () => {
    renderTab()

    const instructionsBox = await screen.findByRole('checkbox', {
      name: 'settings.agentClientsInstructions',
    })
    fireEvent.click(instructionsBox) // default is on; this turn it off
    await pickDemoAndConnect()

    await waitFor(() =>
      expect(setupAgentClients).toHaveBeenCalledWith(1, ['claude'], true, false),
    )
  })

  it('reports every instruction file it wrote, per file', async () => {
    vi.mocked(setupAgentClients).mockResolvedValue({
      code: '',
      blocking: false,
      binary: '/usr/local/bin/repotory-mcp',
      files: [{ client: 'claude', path: '/code/demo/.mcp.json', status: 'created' }],
      instructions: [
        { client: '', path: '/code/demo/AGENTS.md', status: 'updated', note: 'backup: /code/demo/AGENTS.md.repotory-init.bak' },
        { client: '', path: '/code/demo/CLAUDE.md', status: 'created' },
      ],
    })
    renderTab()

    await pickDemoAndConnect()

    // Both files listed, and "updated" is distinguishable from "created": the
    // user is entitled to know their tracked file was edited, not just touched.
    expect(await screen.findByText('/code/demo/AGENTS.md')).toBeTruthy()
    expect(screen.getByText('/code/demo/CLAUDE.md')).toBeTruthy()
    expect(screen.getByText('settings.agentStatusUpdated')).toBeTruthy()
    // Two files were created here (the client config and CLAUDE.md), so the
    // status label legitimately appears twice — asserting the count rather than
    // getByText is what keeps this from being an accident waiting to break.
    expect(screen.getAllByText('settings.agentStatusCreated')).toHaveLength(2)
  })

  it('shows the refusal in the user language, not the backend code', async () => {
    vi.mocked(setupAgentClients).mockResolvedValue({
      code: 'auto_grouped_project',
      blocking: true,
      binary: '',
      files: [],
    })
    renderTab()

    await pickDemoAndConnect()

    expect(await screen.findByText('settings.agentSetupAutoGroupedProject')).toBeTruthy()
  })

  it('lists the files it wrote', async () => {
    renderTab()

    await pickDemoAndConnect()

    expect(await screen.findByText('/code/demo/.mcp.json')).toBeTruthy()
  })
})
