// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'

// Partial mock: override the backend-calling endpoints, keep the rest real.
vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    updateConfig: vi.fn(),
    getAIAuditStatus: vi.fn(),
    listAICallAudit: vi.fn(),
  }
})

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: { defaultValue?: string }) =>
      opts?.defaultValue ?? key,
  }),
}))

import { getAIAuditStatus, listAICallAudit, updateConfig } from '../../api/client'
import AiAuditSection from './AiAuditSection'

const baseStatus = {
  audit_enabled: false,
  review_input: false,
  review_output: false,
  rule_count: 0,
  call_count: 0,
  blocked_count: 0,
  flagged_count: 0,
  surface: 'desktop',
  table_exists: true,
  retention: 'append_only',
}

function renderSection(config: Record<string, string> = {}) {
  return render(
    <AiAuditSection config={config} showMessage={() => {}} />,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getAIAuditStatus).mockResolvedValue({ ...baseStatus })
  vi.mocked(listAICallAudit).mockResolvedValue([])
})

describe('AiAuditSection defaults', () => {
  it('shows every switch off when the config says nothing', async () => {
    renderSection()
    await waitFor(() => expect(getAIAuditStatus).toHaveBeenCalled())

    const audit = screen.getByLabelText('记录调用审计') as HTMLInputElement
    const input = screen.getByLabelText('发出前审查提示词') as HTMLInputElement
    const output = screen.getByLabelText('到达时审查回复') as HTMLInputElement

    expect(audit.checked).toBe(false)
    expect(input.checked).toBe(false)
    expect(output.checked).toBe(false)
  })

  it('reflects the stored config when the audit is already on', async () => {
    renderSection({ ai_audit_enabled: '1', ai_review_input: '1' })
    await waitFor(() => expect(getAIAuditStatus).toHaveBeenCalled())

    expect((screen.getByLabelText('记录调用审计') as HTMLInputElement).checked).toBe(true)
    expect((screen.getByLabelText('发出前审查提示词') as HTMLInputElement).checked).toBe(true)
    expect((screen.getByLabelText('到达时审查回复') as HTMLInputElement).checked).toBe(false)
  })
})

describe('AiAuditSection saving', () => {
  it('writes 1/0 when a switch is toggled', async () => {
    renderSection()
    await waitFor(() => expect(getAIAuditStatus).toHaveBeenCalled())

    fireEvent.click(screen.getByLabelText('记录调用审计'))

    await waitFor(() => {
      expect(updateConfig).toHaveBeenCalledWith('ai_audit_enabled', '1')
    })
  })

  it('writes the rules as one config key', async () => {
    renderSection()
    await waitFor(() => expect(getAIAuditStatus).toHaveBeenCalled())

    const box = screen.getByLabelText('审查规则（每行一个正则，# 开头为注释）') as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: '身份证\n-----BEGIN' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => {
      expect(updateConfig).toHaveBeenCalledWith('ai_review_rules', '身份证\n-----BEGIN')
    })
  })
})

describe('AiAuditSection the call list', () => {
  it('renders one row per recorded call', async () => {
    vi.mocked(listAICallAudit).mockResolvedValue([
      {
        id: 2,
        created_at: '2026-10-25 10:00:00',
        surface: 'mcp',
        endpoint: 'https://api.example.com/v1',
        model: 'gpt-x',
        prompt_hash: 'abc123',
        prompt_chars: 120,
        response_chars: 340,
        input_verdict: 'allow',
        output_verdict: '',
      },
      {
        id: 1,
        created_at: '2026-10-25 09:00:00',
        surface: 'desktop',
        endpoint: 'https://api.example.com/v1',
        model: 'gpt-x',
        prompt_hash: 'def456',
        prompt_chars: 80,
        response_chars: 0,
        input_verdict: 'blocked',
        output_verdict: '',
      },
    ])

    renderSection()
    // findBy* waits for the element instead of polling it — the rows arrive
    // with the audit list, one fetch later than the panel's first paint.
    const table = await screen.findByTestId('ai-audit-table')

    // Counted through the row role rather than querySelectorAll: the header
    // row is a row too, so the count is the two data rows plus that one.
    const rows = within(table).getAllByRole('row')
    expect(rows.length).toBe(3)
    // The blocked one is labelled as such — a blocked call must not look like
    // a clean one in the list.
    expect(screen.getAllByText('已拦截').length).toBe(1)
    expect(screen.getAllByText('通过').length).toBe(1)
  })

  it('shows where the call went and enough of the digest to check the claim', async () => {
    // The panel's own copy promises "time, endpoint, model, surface, size" and
    // that only a hash is kept. Both were fetched and neither was rendered: the
    // table said "trust us" while showing neither the destination nor the hash.
    vi.mocked(listAICallAudit).mockResolvedValue([
      {
        id: 7,
        created_at: '2026-10-25 10:00:00',
        surface: 'mcp',
        endpoint: 'https://api.example.com/v1',
        model: 'gpt-x',
        prompt_hash: 'a1b2c3d4e5f60718',
        prompt_chars: 120,
        response_chars: 340,
        input_verdict: 'allow',
        output_verdict: '',
      },
    ])

    renderSection()
    const table = await screen.findByTestId('ai-audit-table')
    const row = within(table).getAllByRole('row')[1]

    // The endpoint cell carries the full URL in its title even though CSS
    // truncates what is painted.
    const endpoint = within(row).getByTitle('https://api.example.com/v1')
    expect(endpoint.textContent).toBe('https://api.example.com/v1')

    // Eight characters, not the whole digest — and the whole digest on hover.
    const hash = within(row).getByTitle('a1b2c3d4e5f60718')
    expect(hash.textContent).toBe('a1b2c3d4')
  })

  it('does not invent an endpoint or hash for a row that has none', async () => {
    vi.mocked(listAICallAudit).mockResolvedValue([
      {
        id: 8,
        created_at: '2026-10-25 11:00:00',
        surface: 'unknown',
        endpoint: '',
        model: '',
        prompt_hash: '',
        prompt_chars: 0,
        response_chars: 0,
        input_verdict: '',
        output_verdict: '',
      },
    ])

    renderSection()
    const table = await screen.findByTestId('ai-audit-table')
    const row = within(table).getAllByRole('row')[1]

    // A pre-v28 row (or one written while auditing was partly off) has neither;
    // an empty cell is honest, a made-up value is not. Three dashes: endpoint,
    // hash and the verdict column, which is empty for the same reason.
    expect(within(row).getAllByText('—').length).toBe(3)
  })

  it('says so when there is nothing recorded yet', async () => {
    renderSection()
    // findBy* waits for the element instead of polling it — the same
    // convention the call-list tests above use. The empty-state line only
    // renders once the audit LIST has come back (one fetch later than the
    // panel's first paint), so waiting for the status CALL to happen is not
    // the same as waiting for the text to be on screen. On a slow machine
    // the two are far enough apart to fail the assertion.
    expect(await screen.findByText(/还没有调用记录/)).toBeTruthy()
    expect(screen.queryByTestId('ai-audit-table')).toBeNull()
  })
})

describe('AiAuditSection the honest-limits copy', () => {
  it('states that the trail keeps a hash, not the text', async () => {
    renderSection()
    await waitFor(() => expect(getAIAuditStatus).toHaveBeenCalled())

    // The copy is the contract: if this ever stops being shown, the panel
    // would imply it stores prompts.
    expect(screen.getByText(/SHA-256/)).toBeTruthy()
  })

  it('states that streaming output review cannot recall what was shown', async () => {
    renderSection()
    await waitFor(() => expect(getAIAuditStatus).toHaveBeenCalled())

    expect(screen.getByText(/无法撤回/)).toBeTruthy()
  })

  it('states that Repotory ships no built-in blocklist', async () => {
    renderSection()
    await waitFor(() => expect(getAIAuditStatus).toHaveBeenCalled())

    expect(screen.getByText(/不内置任何词表/)).toBeTruthy()
  })

  it('renders the retention policy from the token, not from a backend sentence', async () => {
    // The backend reports the policy as a token ("append_only"). If it ever
    // ships a sentence instead, the English UI would show that sentence
    // verbatim — so the panel must be the only place the wording lives.
    renderSection()
    await waitFor(() => expect(getAIAuditStatus).toHaveBeenCalled())

    expect(screen.getByText(/只增不删/)).toBeTruthy()
  })

  it('says nothing about retention when the policy is unknown', async () => {
    vi.mocked(getAIAuditStatus).mockResolvedValue({ ...baseStatus, retention: 'something_else' })
    renderSection()
    await waitFor(() => expect(getAIAuditStatus).toHaveBeenCalled())

    // An unrecognised policy must not be papered over with the append-only
    // wording — that would claim a guarantee the backend did not make.
    expect(screen.queryByText(/只增不删/)).toBeNull()
  })
})
