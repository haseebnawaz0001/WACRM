/**
 * An automation's path as a tree (plan 08, the builder).
 *
 * A rule is a list of steps. Most steps are actions; a question splits the
 * path into "then" (the contact matches) and "else" (they do not), and a wait
 * pauses it. Everything the canvas does — insert, remove, move, select — is an
 * operation on this tree, done immutably so every edit can be undone.
 */

export type StepKind = 'action' | 'condition' | 'wait'

export interface FlowStep {
  id: string
  type: string
  config: Record<string, any>
  continue_on_error?: boolean
  then?: FlowStep[]
  else?: FlowStep[]
}

/** Where a new step goes: a list (named by its parent and branch) and an index. */
export interface InsertPoint {
  /** The question whose branch this is; null for the top-level path. */
  parentId: string | null
  branch: 'root' | 'then' | 'else'
  index: number
}

export const CONDITION = 'condition'
export const WAIT = 'wait'

export function kindOf(step: Pick<FlowStep, 'type'>): StepKind {
  if (step.type === CONDITION) return 'condition'
  if (step.type === WAIT) return 'wait'
  return 'action'
}

/** A fresh id, short and stable enough to key a card and resume a wait on. */
export function newStepId(): string {
  return `s${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
}

/**
 * A deep copy of a rule's data. Everything in a rule is JSON — it is saved as
 * JSON — and a JSON copy also sees through Vue's reactive proxies, which the
 * draft is full of and structuredClone refuses to copy.
 */
export function copy<T>(value: T): T {
  return value === undefined ? value : JSON.parse(JSON.stringify(value))
}

export function newStep(type: string, config: Record<string, any> = {}): FlowStep {
  const step: FlowStep = { id: newStepId(), type, config: copy(config) }
  if (type === CONDITION) {
    step.config = { filter: { op: 'and', rules: [] }, ...step.config }
    step.then = []
    step.else = []
  }
  if (type === WAIT && !step.config.for) {
    step.config.for = { amount: 1, unit: 'days' }
  }
  return step
}

/** A deep copy, so an edit never mutates a snapshot the undo stack holds. */
export function cloneSteps(steps: FlowStep[]): FlowStep[] {
  return copy(steps)
}

/** Counts every step on every path. */
export function countSteps(steps: FlowStep[] = []): number {
  return steps.reduce((n, s) => n + 1 + countSteps(s.then) + countSteps(s.else), 0)
}

/** How many questions deep the list holding a step sits. */
export function depthOf(steps: FlowStep[], id: string, depth = 0): number | null {
  for (const step of steps) {
    if (step.id === id) return depth
    for (const branch of [step.then, step.else]) {
      if (!branch) continue
      const found = depthOf(branch, id, depth + 1)
      if (found !== null) return found
    }
  }
  return null
}

export function findStep(steps: FlowStep[], id: string): FlowStep | null {
  for (const step of steps) {
    if (step.id === id) return step
    for (const branch of [step.then, step.else]) {
      if (!branch) continue
      const found = findStep(branch, id)
      if (found) return found
    }
  }
  return null
}

/** The list a step lives in, and its index there. */
export function locate(steps: FlowStep[], id: string): { list: FlowStep[]; index: number } | null {
  const index = steps.findIndex(s => s.id === id)
  if (index >= 0) return { list: steps, index }
  for (const step of steps) {
    for (const branch of [step.then, step.else]) {
      if (!branch) continue
      const found = locate(branch, id)
      if (found) return found
    }
  }
  return null
}

/** Resolves an insert point to the actual list inside a (copied) tree. */
function listAt(root: FlowStep[], point: InsertPoint): FlowStep[] | null {
  if (point.parentId === null) return root
  const parent = findStep(root, point.parentId)
  if (!parent) return null
  if (point.branch === 'then') return (parent.then ||= [])
  if (point.branch === 'else') return (parent.else ||= [])
  return null
}

export function insertStep(steps: FlowStep[], point: InsertPoint, step: FlowStep): FlowStep[] {
  const next = cloneSteps(steps)
  const list = listAt(next, point)
  if (!list) return steps
  list.splice(Math.min(point.index, list.length), 0, step)
  return next
}

export function removeStep(steps: FlowStep[], id: string): FlowStep[] {
  const next = cloneSteps(steps)
  const found = locate(next, id)
  if (!found) return steps
  found.list.splice(found.index, 1)
  return next
}

export function updateStep(steps: FlowStep[], id: string, patch: Partial<FlowStep>): FlowStep[] {
  const next = cloneSteps(steps)
  const step = findStep(next, id)
  if (!step) return steps
  Object.assign(step, copy(patch))
  return next
}

/** Moves a step one place up or down within its own list. */
export function moveStep(steps: FlowStep[], id: string, direction: -1 | 1): FlowStep[] {
  const next = cloneSteps(steps)
  const found = locate(next, id)
  if (!found) return steps
  const target = found.index + direction
  if (target < 0 || target >= found.list.length) return steps
  ;[found.list[found.index], found.list[target]] = [found.list[target], found.list[found.index]]
  return next
}

/** Copies a step (and anything under it) with fresh ids, placed right after it. */
export function duplicateStep(steps: FlowStep[], id: string): { steps: FlowStep[]; copyId: string | null } {
  const next = cloneSteps(steps)
  const found = locate(next, id)
  if (!found) return { steps, copyId: null }
  const twin = reId(copy(found.list[found.index]))
  found.list.splice(found.index + 1, 0, twin)
  return { steps: next, copyId: twin.id }
}

function reId(step: FlowStep): FlowStep {
  step.id = newStepId()
  step.then = step.then?.map(reId)
  step.else = step.else?.map(reId)
  return step
}

/** Can a step move up/down, and is it the only thing in its list? */
export function neighbours(steps: FlowStep[], id: string): { canUp: boolean; canDown: boolean } {
  const found = locate(steps, id)
  if (!found) return { canUp: false, canDown: false }
  return { canUp: found.index > 0, canDown: found.index < found.list.length - 1 }
}

/** Every step id on every path, in reading order. */
export function allIds(steps: FlowStep[] = []): string[] {
  return steps.flatMap(s => [s.id, ...allIds(s.then), ...allIds(s.else)])
}
