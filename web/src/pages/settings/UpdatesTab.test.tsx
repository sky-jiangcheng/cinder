// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'

// Partial mock: override the backend-calling endpoints, keep the rest real.
vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    getAppVersion: vi.fn(),
    getInstallInfo: vi.fn(),
    openBrowserURL: vi.fn(),
    updateConfig: vi.fn(),
    runUpdateCheck: vi.fn(),
    subscribeUpdateState: vi.fn(),
    getUpdateState: vi.fn(),
    beginUpdateDownload: vi.fn(),
  }
})

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: { defaultValue?: string; version?: string }) => {
      if (key.endsWith('.currentVersion')) return `Current v${opts?.version ?? ''}`
      if (key.endsWith('.latestVersion')) return `Latest v${opts?.version ?? ''}`
      return opts?.defaultValue ?? key
    },
  }),
}))

import {
  beginUpdateDownload, getAppVersion, getInstallInfo, getUpdateState, openBrowserURL,
  runUpdateCheck, subscribeUpdateState, updateConfig,
} from '../../api/client'
import UpdatesTab from './UpdatesTab'

const noUpdate = {
  current: '2.12.3',
  latest: '2.12.3',
  has_update: false,
  release_url: 'https://example.invalid/release',
  published_at: '2026-10-24T10:00:00Z',
  notes: '',
  assets: [],
  download_url: '',
  download_name: '',
  download_sha256: '',
  download_size: 0,
}

const newerVersion = {
  ...noUpdate,
  latest: '2.13.0',
  has_update: true,
  download_url: 'https://example.invalid/repotory-darwin-arm64.dmg',
  download_name: 'repotory-darwin-arm64.dmg',
  download_sha256: 'sha256:abc',
  download_size: 1234,
  notes: '- **shiny** new thing',
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getAppVersion).mockResolvedValue('2.12.3')
  vi.mocked(getInstallInfo).mockResolvedValue({
    version: '2.12.3',
    platform: 'darwin',
    install_path: '/Applications/Repotory.app',
    executable_path: '/Applications/Repotory.app/Contents/MacOS/repotory',
    download_dir: '/Users/me/Downloads',
  })
  vi.mocked(getUpdateState).mockReturnValue({ phase: 'idle', info: null, error: '' })
  vi.mocked(subscribeUpdateState).mockReturnValue(() => {})
  vi.mocked(beginUpdateDownload).mockResolvedValue(undefined)
})

function renderTab(config: Record<string, string> = {}) {
  return render(<UpdatesTab initialConfig={config} showMessage={() => {}} />)
}

describe('UpdatesTab', () => {
  it('shows the running version before any check has run', async () => {
    renderTab()
    // The version comes from a local read, not from GitHub: a card that could
    // only state your version after a network round trip would sit blank on
    // the very machine that needs the proxy field.
    expect(await screen.findByText('Current v2.12.3')).toBeTruthy()
  })

  it('runs a check when the user clicks the button', async () => {
    renderTab()
    fireEvent.click(screen.getByRole('button', { name: 'settings.updates.check' }))
    await waitFor(() => expect(runUpdateCheck).toHaveBeenCalled())
  })

  it('downloads the platform bundle in-app instead of handing it to the browser', async () => {
    vi.mocked(getUpdateState).mockReturnValue({ phase: 'available', info: newerVersion, error: '' })
    renderTab()
    fireEvent.click(screen.getByRole('button', { name: 'settings.updates.download' }))
    await waitFor(() =>
      expect(beginUpdateDownload).toHaveBeenCalledWith(
        'https://example.invalid/repotory-darwin-arm64.dmg',
        'repotory-darwin-arm64.dmg',
        'sha256:abc',
        // The version travels with the request: it goes into the file name
        // and into the progress snapshot.
        '2.13.0',
      ),
    )
    // The browser is NOT involved: the app fetches and verifies the bundle
    // itself, which is what buys the progress readout and the checksum.
    expect(openBrowserURL).not.toHaveBeenCalled()
  })

  it('falls back to the release page when the release has no bundle for this platform', async () => {
    vi.mocked(getUpdateState).mockReturnValue({
      phase: 'available',
      info: { ...newerVersion, download_url: '', download_name: '' },
      error: '',
    })
    renderTab()
    fireEvent.click(screen.getByRole('button', { name: 'settings.updates.download' }))
    await waitFor(() => expect(openBrowserURL).toHaveBeenCalledWith('https://example.invalid/release'))
  })

  it('shows where the app is installed and where bundles are written', async () => {
    renderTab()
    // The two questions a user has after an install: "where did it install?"
    // and "where did the download go?". Both have an answer now.
    expect(await screen.findByText('/Applications/Repotory.app')).toBeTruthy()
    expect(await screen.findByText('/Users/me/Downloads')).toBeTruthy()
  })

  it('persists the auto-check switch under update_check_enabled', async () => {
    renderTab({ update_check_enabled: '1' })
    fireEvent.click(screen.getByRole('button', { name: 'settings.updates.autoCheckLabel' }))
    await waitFor(() =>
      expect(updateConfig).toHaveBeenCalledWith('update_check_enabled', '0'),
    )
  })

  it('persists the proxy under update_proxy', async () => {
    renderTab()
    fireEvent.change(screen.getByLabelText('settings.updates.proxyLabel'), {
      target: { value: 'http://127.0.0.1:7890' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'settings.save' }))
    await waitFor(() => expect(updateConfig).toHaveBeenCalledWith('update_proxy', 'http://127.0.0.1:7890'))
  })
})
