/**
 * Centralized Chart.js setup
 * Import this module in components that need charts to ensure registration happens once
 */
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  BarElement,
  ArcElement,
  Title,
  Tooltip,
  Legend,
  Filler
} from 'chart.js'

// Register Chart.js components once
ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  BarElement,
  ArcElement,
  Title,
  Tooltip,
  Legend,
  Filler
)

// Set default options for better tooltip behavior
// This makes tooltips show when hovering near data points, not just exactly on them
ChartJS.defaults.interaction.mode = 'index'
ChartJS.defaults.interaction.intersect = false

// Re-export chart components for convenience
export { Line, Bar, Pie, Doughnut } from 'vue-chartjs'
export { ChartJS }

/* ------------------------------------------------------------------------- *
 * Theming
 *
 * Chart.js ships its own greys, its own legend and its own tick formatting,
 * and none of them belong to this design system: on the dark surface its
 * default labels sit below readable contrast, its legend swatch is a 40px
 * slab, and a count axis gets decimal ticks (3.0, 2.5, 2.0) for values that
 * can only ever be whole. Charts are the largest thing on the dashboard, so
 * those defaults were also the largest unstyled surface in the app.
 * ------------------------------------------------------------------------- */

/**
 * The palette for categories that carry no meaning of their own — a source, a
 * template, a stage name. Ordered so neighbours stay apart when a chart only
 * uses the first three.
 */
const categorical = [
  '#3b82f6', // blue
  '#8b5cf6', // violet
  '#06b6d4', // cyan
  '#ec4899', // pink
  '#f59e0b', // amber
  '#14b8a6', // teal
  '#a855f7', // purple
  '#0ea5e9'  // sky
]

/**
 * Outcomes, which do carry meaning.
 *
 * A status series was being coloured by position, so whichever outcome happened
 * to sort first took blue and the second took green — which is how "failed"
 * came out green in Messages by Status and "cancelled" green in Chatbot
 * Sessions. A chart that paints a failure in success green is worse than one
 * with no colour at all, because it is read at a glance and believed.
 */
const semantic: Record<string, string> = {
  // Arrived, finished, succeeded.
  read: '#10b981',
  delivered: '#10b981',
  completed: '#10b981',
  resolved: '#10b981',
  won: '#10b981',
  sent: '#3b82f6',
  received: '#3b82f6',
  open: '#3b82f6',
  active: '#3b82f6',
  // Waiting on somebody.
  pending: '#f59e0b',
  queued: '#f59e0b',
  snoozed: '#f59e0b',
  timeout: '#f59e0b',
  // Went wrong.
  failed: '#ef4444',
  error: '#ef4444',
  rejected: '#ef4444',
  lost: '#ef4444',
  // Stopped on purpose — not a failure, not a success.
  cancelled: '#64748b',
  canceled: '#64748b',
  abandoned: '#64748b',
  expired: '#64748b',
  unknown: '#64748b',
  '(empty)': '#64748b'
}

/** The colour for one slice or bar, by what it means where that is known. */
export function chartColor(label: string, index: number): string {
  const key = String(label ?? '').trim().toLowerCase()
  return semantic[key] ?? categorical[index % categorical.length]
}

/** Colours for a whole series, in order. */
export function chartColors(labels: string[]): string[] {
  return labels.map((l, i) => chartColor(l, i))
}

/** Whether the app is currently in light mode. */
function isLight(): boolean {
  return document.documentElement.classList.contains('light')
}

/**
 * The ink charts are drawn in.
 *
 * Read at call time rather than frozen at module load, so a chart built after
 * the theme changes is drawn in the theme that is actually on screen.
 */
export function chartInk() {
  const light = isLight()
  return {
    text: light ? '#4b5563' : 'rgba(255,255,255,0.55)',
    grid: light ? 'rgba(0,0,0,0.06)' : 'rgba(255,255,255,0.06)',
    tooltipBg: light ? '#ffffff' : '#18181b',
    tooltipText: light ? '#111827' : '#fafafa',
    tooltipBorder: light ? 'rgba(0,0,0,0.1)' : 'rgba(255,255,255,0.12)'
  }
}

/**
 * Options for a bar or line chart.
 *
 * `singleSeries` hides the legend: with one dataset the legend prints the
 * widget's own title back at you, directly under the widget's own title.
 */
export function barLineOptions(opts: { singleSeries?: boolean; integer?: boolean } = {}) {
  const ink = chartInk()
  return {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { mode: 'index' as const, intersect: false },
    plugins: {
      legend: opts.singleSeries
        ? { display: false }
        : {
            display: true,
            position: 'top' as const,
            align: 'end' as const,
            labels: {
              color: ink.text,
              usePointStyle: true,
              pointStyle: 'circle' as const,
              boxWidth: 8,
              boxHeight: 8,
              padding: 16,
              font: { size: 11 }
            }
          },
      tooltip: tooltipStyle(ink)
    },
    scales: {
      x: {
        border: { display: false },
        grid: { display: false },
        ticks: { color: ink.text, font: { size: 11 }, maxRotation: 0, autoSkipPadding: 12 }
      },
      y: {
        beginAtZero: true,
        border: { display: false },
        grid: { color: ink.grid, drawTicks: false },
        ticks: {
          color: ink.text,
          font: { size: 11 },
          padding: 8,
          // A count has no halves. Chart.js picks decimal steps whenever the
          // range is small, which is exactly when counts are small.
          precision: opts.integer === false ? undefined : 0
        }
      }
    }
  }
}

/** Options for a pie or doughnut. */
export function pieOptions() {
  const ink = chartInk()
  return {
    responsive: true,
    maintainAspectRatio: false,
    // A ring rather than a disc: the hole keeps the slices closer to the same
    // radius, which is where the eye compares them, and stops one 85% slice
    // reading as a solid circle with a nick out of it.
    cutout: '58%',
    plugins: {
      legend: {
        position: 'bottom' as const,
        labels: {
          color: ink.text,
          usePointStyle: true,
          pointStyle: 'circle' as const,
          boxWidth: 8,
          boxHeight: 8,
          padding: 14,
          font: { size: 11 }
        }
      },
      tooltip: tooltipStyle(ink)
    }
  }
}

function tooltipStyle(ink: ReturnType<typeof chartInk>) {
  return {
    backgroundColor: ink.tooltipBg,
    titleColor: ink.tooltipText,
    bodyColor: ink.tooltipText,
    borderColor: ink.tooltipBorder,
    borderWidth: 1,
    padding: 10,
    cornerRadius: 8,
    displayColors: true,
    boxWidth: 8,
    boxHeight: 8,
    usePointStyle: true
  }
}
