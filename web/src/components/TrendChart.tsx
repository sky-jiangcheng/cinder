import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Line } from 'react-chartjs-2'
import s from './TrendChart.module.css'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Title,
} from 'chart.js'
import { cssVar } from '../utils/theme'
import { useTheme } from '../hooks/useTheme'

// 预留钩子类：`chart-simple` —— 目前全站没有对应样式定义（P35 复核确认），
// 保留在 markup 里作为将来挂样式的锚点，删留都不影响行为。

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend, Title)

export interface TrendDataset {
  label: string
  data: number[]
  /** Token name (e.g. '--success') resolved against the active theme. */
  color: string
}

interface Props {
  labels: string[]
  datasets: TrendDataset[]
  /** Pixel height; the overview embeds a shorter version of the same chart. */
  height?: number
}

function TrendChart({ labels, datasets, height = 220 }: Props) {
  const { t } = useTranslation()
  // Canvas paints with resolved values, not CSS — so a theme flip needs a
  // re-render to repaint the axes, grid and tooltip in the new palette.
  useTheme()

  const { data, options } = useMemo(() => {
    const border = cssVar('--border-subtle')
    const text = cssVar('--text-tertiary')
    const textStrong = cssVar('--text-secondary')
    const grid = cssVar('--border-subtle')
    const tooltipBg = cssVar('--bg-secondary')

    const resolved = datasets.map((ds) => ({
      ...ds,
      // The token resolves to the light or dark value; fall back to the raw
      // value if a caller passes a literal colour instead of a token.
      borderColor: ds.color.startsWith('--') ? cssVar(ds.color) || '#4a7d4a' : ds.color,
    }))

    // A "全部" window can carry 300+ days. Chart.js draws a point per label by
    // default, so the series became a solid band of 2px dots and Chart.js
    // defaulted borderWidth to 3 — the line read as a filled block, not a
    // trend. Thin stroke, no per-point markers, and a soft fill so the shape
    // still reads at 300 samples.
    return {
      data: {
        labels,
        datasets: resolved.map((ds) => ({
          ...ds,
          backgroundColor: ds.borderColor + '14',
          fill: true,
          tension: 0.25,
          borderWidth: 1.25,
          pointRadius: 0,
          pointHoverRadius: 3,
          pointHitRadius: 12,
        })),
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        interaction: { mode: 'index' as const, intersect: false },
        plugins: {
          legend: {
            position: 'top' as const,
            align: 'end' as const,
            labels: {
              color: textStrong,
              // Chart.js's default 16px legend type is heavy at this size and
              // the colour swatch plus label already carry the meaning.
              boxWidth: 8,
              boxHeight: 8,
              usePointStyle: true,
              pointStyle: 'circle' as const,
              font: { size: 12 },
            },
          },
          tooltip: {
            backgroundColor: tooltipBg,
            titleColor: textStrong,
            bodyColor: textStrong,
            borderColor: border,
            borderWidth: 1,
            cornerRadius: 6,
            padding: 10,
            // Match the app's surface elevation so the tooltip doesn't read
            // as a foreign layer pasted over the chart.
            boxPadding: 4,
          },
        },
        scales: {
          y: {
            beginAtZero: true,
            // Horizontal-only grid: vertical lines add noise without carrying
            // a value, and this is an editorial layout, not a dense plot.
            grid: { color: grid, drawTicks: false },
            border: { display: false },
            ticks: {
              color: text,
              padding: 8,
              font: { size: 11 },
              maxTicksLimit: 5,
            },
            title: {
              display: true,
              text: t('trend.count', { defaultValue: 'Count' }),
              color: text,
              font: { size: 11 },
            },
          },
          x: {
            grid: { display: false },
            border: { color: border },
            ticks: {
              color: text,
              padding: 8,
              font: { size: 11 },
              // A day per label is unreadable past a few dozen: Chart.js would
              // overlap them into a smear. 6 keeps the date legible and spaced
              // regardless of how many samples the window carries.
              maxTicksLimit: 6,
              maxRotation: 0,
              autoSkip: true,
              autoSkipPadding: 16,
            },
            title: {
              display: true,
              text: t('summaryBar.date', { defaultValue: 'Date' }),
              color: text,
              font: { size: 11 },
            },
          },
        },
      },
    }
  }, [labels, datasets, t])

  return (
    <div className={s.container} style={{ height }}>
      <Line data={data} options={options} className="chart-simple" />
    </div>
  )
}

export default TrendChart
