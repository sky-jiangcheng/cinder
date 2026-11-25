// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import type { ProjectOverview } from '../../api/client'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: { defaultValue?: string }) =>
      key === 'project.readme' ? 'README' : opts?.defaultValue ?? key,
  }),
}))

vi.mock('../../components/TrendChart', () => ({
  default: ({ labels }: { labels: string[] }) => <div data-testid="trend-chart">{labels.length} points</div>,
}))

import ProjectOverviewSection from './ProjectOverviewSection'

function overview(overrides: Partial<ProjectOverview> = {}): ProjectOverview {
  return {
    readme_excerpt: '',
    tech_stack: [],
    languages: [],
    dependencies: [],
    top_contributors: [],
    activity: null,
    recent_commits: [],
    cached: true,
    ...overrides,
  }
}

describe('ProjectOverviewSection', () => {
  it('renders activity metrics even when there is no chart or mined profile', () => {
    render(
      <ProjectOverviewSection
        overview={overview({
          activity: {
            total_commits: 27287,
            commit_rate_30d: 121,
            active_days: 23,
            active_months: 4,
            last_commit_date: '2026-11-25',
          },
        })}
      />,
    )

    expect(screen.getByText('27,287')).toBeTruthy()
    expect(screen.getByText('121')).toBeTruthy()
    expect(screen.queryByText('project.overviewEmpty')).toBeNull()
  })

  it('keeps README and dependencies collapsed until requested', () => {
    render(
      <ProjectOverviewSection
        overview={overview({
          readme_excerpt: '# Payment service',
          dependencies: [{ name: 'spring-boot', version: '3.5.0', source: 'maven' }],
        })}
      />,
    )

    const [readme, dependencies] = screen.getAllByRole('group') as HTMLDetailsElement[]
    expect(readme.open).toBe(false)
    expect(dependencies.open).toBe(false)

    fireEvent.click(screen.getByText('README'))
    expect(readme.open).toBe(true)
    expect(screen.getByRole('heading', { name: 'Payment service' })).toBeTruthy()
  })

  it('shows selected-range totals beside the trend', () => {
    render(
      <ProjectOverviewSection
        overview={overview({
          activity: {
            total_commits: 10,
            commit_rate_30d: 2,
            active_days: 3,
            active_months: 1,
            last_commit_date: '2026-11-25',
          },
        })}
        trend={{ labels: ['2026-11-25'], datasets: [{ label: 'Commits', data: [2], color: '--success' }] }}
        scope="week"
        totals={{ added: 700, deleted: 0, files: 23, active: 1 }}
      />,
    )

    expect(screen.getByTestId('trend-chart')).toBeTruthy()
    expect(screen.getByText('+700')).toBeTruthy()
    expect(screen.getByText('−0')).toBeTruthy()
    expect(screen.getByLabelText('project.selectedRange')).toBeTruthy()
  })
})
