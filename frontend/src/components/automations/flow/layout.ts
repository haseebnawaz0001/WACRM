/**
 * Lays a rule's path out on the canvas.
 *
 * Nobody drags anything here. Free-form node editors ask the person to be the
 * layout engine — to place boxes and wire arrows — which is exactly the part a
 * front-desk manager should not have to learn. The path is drawn from the
 * tree: steps stack top to bottom, a question splits into Yes (left) and No
 * (right) columns that rejoin below it, and every connector carries the spot
 * where a new step would go, so "add a step here" is a button on the line.
 */
import type { Edge, Node } from '@vue-flow/core'
import { CONDITION, WAIT, type FlowStep, type InsertPoint } from './tree'

export const CARD_WIDTH = 296
const GAP_X = 56
const GAP_Y = 64
/** Room between a question and its branches for the Yes/No labels and +. */
const BRANCH_GAP_Y = 112

const HEIGHT: Record<string, number> = {
  start: 100,
  action: 64,
  condition: 64,
  wait: 52,
  end: 36,
  join: 12,
  empty: 36
}

/** Card heights the canvas measured from the words on them. */
export interface Measured {
  start?: number
  step?: (step: FlowStep) => number | undefined
}

/** How wide a list's column has to be to hold everything in it. */
function listWidth(steps: FlowStep[] = []): number {
  let width = CARD_WIDTH
  for (const step of steps) {
    if (step.type === CONDITION) {
      const split = Math.max(CARD_WIDTH, listWidth(step.then)) + GAP_X + Math.max(CARD_WIDTH, listWidth(step.else))
      width = Math.max(width, split)
    }
  }
  return width
}

export interface FlowEdgeData {
  insert?: InsertPoint
  label?: 'yes' | 'no'
  /** A branch edge leaves a question sideways; the + sits at its foot. */
  branch?: boolean
}

export interface LaidOut {
  nodes: Node[]
  edges: Edge<FlowEdgeData>[]
  width: number
  height: number
}

interface Cursor {
  /** The node the next edge leaves from. */
  from: string
  y: number
}

/**
 * Lays out the whole path under a start card. Node data carries only what
 * the layout knows (kind, step); the canvas adds sentences and state.
 */
export function layoutFlow(steps: FlowStep[], measured: Measured = {}): LaidOut {
  const heightOf = (step: FlowStep): number => {
    if (step.type === WAIT) return HEIGHT.wait
    return measured.step?.(step) ?? (step.type === CONDITION ? HEIGHT.condition : HEIGHT.action)
  }
  const startHeight = measured.start ?? HEIGHT.start
  const nodes: Node[] = []
  const edges: Edge<FlowEdgeData>[] = []
  const width = listWidth(steps)
  const centerX = width / 2

  const place = (id: string, type: string, x: number, y: number, h: number, data: Record<string, any>) => {
    const w = type === 'join' ? 12 : type === 'end' ? 96 : type === 'empty' ? 168 : CARD_WIDTH
    nodes.push({
      id, type, data, position: { x: x - w / 2, y },
      draggable: false, connectable: false, selectable: type !== 'join' && type !== 'end',
      style: { width: `${w}px`, height: `${h}px` }
    })
  }

  let edgeSeq = 0
  const connect = (source: string, target: string, data: FlowEdgeData) => {
    edges.push({ id: `e${edgeSeq++}`, source, target, type: 'flow', data })
  }

  /**
   * Places one list in a column. Returns where the path continues below it.
   * `parentId`/`branch` name the list for insert points.
   */
  const layoutList = (
    list: FlowStep[], cx: number, start: Cursor,
    parentId: string | null, branch: InsertPoint['branch'],
    firstEdge?: Pick<FlowEdgeData, 'label' | 'branch'>
  ): Cursor => {
    let cursor = start
    list.forEach((step, index) => {
      const edgeExtras = index === 0 ? firstEdge : undefined
      const y = cursor.y
      place(step.id, step.type === CONDITION ? 'condition' : step.type === WAIT ? 'wait' : 'action',
        cx, y, heightOf(step), { step })
      connect(cursor.from, step.id, { insert: { parentId, branch, index }, ...edgeExtras })

      if (step.type !== CONDITION) {
        cursor = { from: step.id, y: y + heightOf(step) + GAP_Y }
        return
      }

      // A question: two columns that meet again underneath.
      const wThen = Math.max(CARD_WIDTH, listWidth(step.then))
      const wElse = Math.max(CARD_WIDTH, listWidth(step.else))
      const total = wThen + GAP_X + wElse
      const leftX = cx - total / 2 + wThen / 2
      const rightX = cx + total / 2 - wElse / 2
      const branchY = y + heightOf(step) + BRANCH_GAP_Y

      const joinId = `${step.id}__join`
      const yes = layoutBranch(step.then || [], leftX, { from: step.id, y: branchY }, step.id, 'then', 'yes')
      const no = layoutBranch(step.else || [], rightX, { from: step.id, y: branchY }, step.id, 'else', 'no')
      const joinY = Math.max(yes.y, no.y)
      place(joinId, 'join', cx, joinY, HEIGHT.join, { question: step.id })
      // An empty branch already offers its + on the edge into its
      // placeholder; a second one on the way out would be the same spot.
      connect(yes.from, joinId, (step.then || []).length
        ? { insert: { parentId: step.id, branch: 'then', index: step.then!.length } }
        : {})
      connect(no.from, joinId, (step.else || []).length
        ? { insert: { parentId: step.id, branch: 'else', index: step.else!.length } }
        : {})
      cursor = { from: joinId, y: joinY + HEIGHT.join + GAP_Y }
    })
    return cursor
  }

  /**
   * A branch column. An empty branch still gets its own spot — a small
   * "nothing yet" marker in its column — so its Yes or No edge has somewhere
   * to point and its + does not sit on top of the other branch's.
   */
  const layoutBranch = (
    list: FlowStep[], cx: number, start: Cursor, parentId: string, branch: 'then' | 'else', label: 'yes' | 'no'
  ): Cursor => {
    if (!list.length) {
      const emptyId = `${parentId}__${branch}__empty`
      place(emptyId, 'empty', cx, start.y, HEIGHT.empty, { question: parentId, branch })
      connect(start.from, emptyId, { insert: { parentId, branch, index: 0 }, label, branch: true })
      return { from: emptyId, y: start.y + HEIGHT.empty + GAP_Y / 2 }
    }
    return layoutList(list, cx, start, parentId, branch, { label, branch: true })
  }

  place('start', 'start', centerX, 0, startHeight, {})
  const end = layoutList(steps, centerX, { from: 'start', y: startHeight + GAP_Y }, null, 'root')
  place('end', 'end', centerX, end.y, HEIGHT.end, {})
  connect(end.from, 'end', { insert: { parentId: null, branch: 'root', index: steps.length } })

  return { nodes, edges, width, height: end.y + HEIGHT.end }
}
