// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'

// Partial mock: the download store's actions are stubbed so the test does
// not talk to a backend; the formatting helpers stay real.
vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    abortUpdateDownload: vi.fn(),
    ejectInstallerVolume: vi.fn(),
    getInstallInfo: vi.fn(),
    openDownloadedUpdate: vi.fn(),
    openDownloadModal: vi.fn(),
    revealDownloadedUpdate: vi.fn(),
    minimizeDownloadModal: vi.fn(),
    subscribeDownloadState: vi.fn(),
    syncDownloadState: vi.fn(),
  }
})

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) => {
      // The path and the version are interpolated so the tests can assert
      // that the dialog actually SHOWS them, not just that a label exists.
      if (opts && 'version' in opts) return `${key}:${opts.version}`
      if (opts && 'path' in opts) return `${key}:${opts.path}`
      return key
    },
  }),
}))

import {
  abortUpdateDownload,
  ejectInstallerVolume,
  getInstallInfo,
  openDownloadedUpdate,
  revealDownloadedUpdate,
  subscribeDownloadState,
  syncDownloadState,
  type DownloadSnapshot,
} from '../api/client'
import UpdateDownloadUI, { formatBytes, formatDuration } from './UpdateDownloadUI'

const running: DownloadSnapshot = {
  modalOpen: true,
  state: {
    phase: 'running',
    version: '2.13.1',
    file_name: 'repotory-darwin-arm64.dmg',
    downloaded: 50 * 1024 * 1024,
    total: 100 * 1024 * 1024,
    speed: 2 * 1024 * 1024,
    remaining_seconds: 25,
    path: '',
    error: '',
    checksum_verified: false,
    installer_ejected: false,
  },
}

const done: DownloadSnapshot = {
  modalOpen: true,
  state: {
    ...running.state,
    phase: 'done',
    downloaded: 100 * 1024 * 1024,
    remaining_seconds: 0,
    path: '/tmp/repotory-darwin-arm64.dmg',
    checksum_verified: true,
  },
}

const idle: DownloadSnapshot = {
  modalOpen: false,
  state: {
    phase: 'idle',
    version: '',
    file_name: '',
    downloaded: 0,
    total: 0,
    speed: 0,
    remaining_seconds: -1,
    path: '',
    error: '',
    checksum_verified: false,
    installer_ejected: false,
  },
}

let current: DownloadSnapshot = idle

beforeEach(() => {
  vi.clearAllMocks()
  current = idle
  vi.mocked(subscribeDownloadState).mockImplementation((cb: (s: DownloadSnapshot) => void) => {
    cb(current)
    return () => {}
  })
  vi.mocked(syncDownloadState).mockResolvedValue(idle.state)
  vi.mocked(getInstallInfo).mockResolvedValue({
    version: '2.13.1',
    platform: 'darwin',
    install_path: '/Applications/Cinder.app',
    executable_path: '/Applications/Cinder.app/Contents/MacOS/repotory',
    download_dir: '/Users/me/Downloads',
  })
  vi.mocked(ejectInstallerVolume).mockResolvedValue(true)
  vi.mocked(revealDownloadedUpdate).mockResolvedValue(undefined)
})

describe('UpdateDownloadUI', () => {
  it('renders nothing when no download has started', () => {
    render(<UpdateDownloadUI />)
    // Neither surface may exist: an idle app must not show a stray progress
    // strip in the corner.
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.queryByRole('button', { name: 'settings.updates.downloadExpand' })).toBeNull()
  })

  it('shows the modal with the percentage, bytes, speed and ETA', () => {
    current = running
    render(<UpdateDownloadUI />)
    // The percentage comes from the byte counters, not from the backend, so
    // the bar and the number can never disagree.
    expect(screen.getByText('50%')).toBeTruthy()
    expect(screen.getByText('settings.updates.downloadBytes')).toBeTruthy()
    expect(screen.getByText('settings.updates.downloadSpeed')).toBeTruthy()
    expect(screen.getByText('settings.updates.downloadRemaining')).toBeTruthy()
  })

  it('offers minimize and cancel while the download runs', () => {
    current = running
    render(<UpdateDownloadUI />)
    expect(screen.getByRole('button', { name: 'settings.updates.downloadMinimize' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'settings.updates.downloadCancel' })).toBeTruthy()
  })

  it('cancels the download when the cancel button is pressed', async () => {
    current = running
    render(<UpdateDownloadUI />)
    fireEvent.click(screen.getByRole('button', { name: 'settings.updates.downloadCancel' }))
    await waitFor(() => expect(abortUpdateDownload).toHaveBeenCalled())
  })

  it('offers to open the installer once the download is done and verified', () => {
    current = done
    render(<UpdateDownloadUI />)
    expect(screen.getByText('settings.updates.downloadDoneVerified')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'settings.updates.downloadOpenInstaller' }))
    expect(openDownloadedUpdate).toHaveBeenCalledWith('/tmp/repotory-darwin-arm64.dmg')
  })

  it('shows the corner card instead of the modal once minimized', () => {
    current = { ...running, modalOpen: false }
    render(<UpdateDownloadUI />)
    // The corner card is the minimized form: the progress must still be
    // visible, just without the modal taking over the screen.
    expect(screen.getByRole('button', { name: 'settings.updates.downloadExpand' })).toBeTruthy()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  // The finished state used to say "download complete" and nothing else, so
  // the user could not tell where the file went, which version it was, or
  // how to get rid of the mounted installer volume.
  it('shows the full path of the finished bundle', () => {
    current = done
    render(<UpdateDownloadUI />)
    expect(screen.getByText(/repotory-darwin-arm64\.dmg/)).toBeTruthy()
  })

  it('names the version in the finished dialog', () => {
    current = done
    render(<UpdateDownloadUI />)
    expect(screen.getByText(/2\.13\.1/)).toBeTruthy()
  })

  it('reveals the bundle in the file manager', () => {
    current = done
    render(<UpdateDownloadUI />)
    fireEvent.click(screen.getByRole('button', { name: 'settings.updates.downloadReveal' }))
    expect(revealDownloadedUpdate).toHaveBeenCalledWith('/tmp/repotory-darwin-arm64.dmg')
  })

  it('offers to eject the installer volume on macOS', async () => {
    current = done
    render(<UpdateDownloadUI />)
    const button = await screen.findByRole('button', { name: 'settings.updates.downloadEject' })
    fireEvent.click(button)
    await waitFor(() => expect(ejectInstallerVolume).toHaveBeenCalled())
  })

  it('does not offer the eject action where no image is mounted', async () => {
    vi.mocked(getInstallInfo).mockResolvedValue({
      version: '2.13.1',
      platform: 'linux',
      install_path: '/usr/bin/repotory',
      executable_path: '/usr/bin/repotory',
      download_dir: '/home/me/Downloads',
    })
    current = done
    render(<UpdateDownloadUI />)
    // The platform arrives asynchronously; wait for the dialog to settle.
    await waitFor(() => expect(getInstallInfo).toHaveBeenCalled())
    expect(screen.queryByRole('button', { name: 'settings.updates.downloadEject' })).toBeNull()
  })

  it('reports when there was no installer volume to eject', async () => {
    vi.mocked(ejectInstallerVolume).mockResolvedValue(false)
    current = done
    render(<UpdateDownloadUI />)
    const button = await screen.findByRole('button', { name: 'settings.updates.downloadEject' })
    fireEvent.click(button)
    await waitFor(() => expect(ejectInstallerVolume).toHaveBeenCalled())
    // "Nothing was mounted" is a real answer, and it is not the same answer
    // as "the volume is gone".
    expect(await screen.findByText('settings.updates.downloadEjectNone')).toBeTruthy()
  })

  it('confirms the eject when a volume was actually unmounted', async () => {
    vi.mocked(ejectInstallerVolume).mockResolvedValue(true)
    current = done
    render(<UpdateDownloadUI />)
    const button = await screen.findByRole('button', { name: 'settings.updates.downloadEject' })
    fireEvent.click(button)
    await waitFor(() => expect(ejectInstallerVolume).toHaveBeenCalled())
    expect(await screen.findByText('settings.updates.downloadEjected')).toBeTruthy()
  })
})

describe('formatBytes', () => {
  it('picks the largest readable unit', () => {
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(1024)).toBe('1 KB')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(50 * 1024 * 1024)).toBe('50 MB')
  })

  it('treats an unknown size as zero', () => {
    expect(formatBytes(0)).toBe('0 B')
  })
})

describe('formatDuration', () => {
  // The unit wording is locale data; the defaultValue carries the Chinese form
  // so this suite keeps asserting the Chinese rendering, while the app reads
  // `common.duration*` from the active locale.
  const t = (key: string, opts?: Record<string, unknown>) =>
    (opts?.defaultValue as string | undefined) ?? key

  it('renders seconds, minutes and hours', () => {
    expect(formatDuration(t, 45)).toBe('45秒')
    expect(formatDuration(t, 83)).toBe('1分23秒')
    expect(formatDuration(t, 7500)).toBe('2小时5分')
  })

  it('marks an unknown ETA', () => {
    expect(formatDuration(t, -1)).toBe('—')
  })
})
