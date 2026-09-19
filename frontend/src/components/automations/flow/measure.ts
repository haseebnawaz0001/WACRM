/**
 * How many lines a card's words will take, before the card is drawn.
 *
 * The layout places cards by height, and a card sized for the longest
 * possible sentence leaves most of them with a band of empty space under a
 * one-line detail. Measuring the actual words with the page's own font keeps
 * each card as tall as what it says.
 */

let context: CanvasRenderingContext2D | null | undefined
let family = ''

function ctx(): CanvasRenderingContext2D | null {
  if (context === undefined) {
    context = typeof document === 'undefined' ? null : document.createElement('canvas').getContext('2d')
    family = typeof document === 'undefined' ? 'sans-serif' : getComputedStyle(document.body).fontFamily || 'sans-serif'
  }
  return context
}

/** Lines `text` wraps to at `width`, never more than `max`. */
export function lineCount(text: string, size: number, weight: number, width: number, max: number): number {
  const clean = text.replace(/\s+/g, ' ').trim()
  if (!clean) return 0
  const c = ctx()
  if (!c) return Math.min(max, Math.ceil((clean.length * size * 0.55) / width))
  c.font = `${weight} ${size}px ${family}`
  let lines = 1
  let line = 0
  const space = c.measureText(' ').width
  for (const word of clean.split(' ')) {
    const w = c.measureText(word).width
    if (line > 0 && line + space + w <= width) {
      line += space + w
      continue
    }
    if (line > 0) lines++
    // A word longer than the line (a URL) breaks where it has to.
    lines += Math.max(0, Math.ceil(w / width) - 1)
    line = w % width || w
    if (lines >= max) return max
  }
  return Math.min(lines, max)
}
