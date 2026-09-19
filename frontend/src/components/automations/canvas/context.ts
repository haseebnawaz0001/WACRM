/**
 * What the canvas's nodes and edges can ask of the builder. Nodes are
 * rendered by Vue Flow, not by the builder, so they reach it through
 * provide/inject rather than events.
 */
import type { InjectionKey, Ref } from 'vue'
import type { InsertPoint } from '../flow/tree'
import type { Part } from '../flow/sentences'

export interface TraceEntry {
  status: 'succeeded' | 'failed' | 'skipped' | 'waiting'
  branch?: 'then' | 'else'
  error?: string
  until?: string
  dryRun?: boolean
}

export interface FlowContext {
  editable: Ref<boolean>
  /** Action types the backend offers. */
  available: Ref<string[]>
  /** Why a step cannot go here, or null when it can. */
  blockedReason: (point: InsertPoint, type: string) => string | null
  insert: (point: InsertPoint, type: string) => void
  remove: (id: string) => void
  duplicate: (id: string) => void
  select: (id: string | null) => void
  /** Clears the selected card without closing whatever panel is open. */
  deselect: () => void
}

export const flowContextKey: InjectionKey<FlowContext> = Symbol('automation-flow')

/** Per-node presentation the canvas computes and hands to each node. */
export interface NodeView {
  title: string
  detail?: string
  /** The detail in runs, the chosen values marked so they carry weight. */
  parts?: Part[]
  problem?: string
  waiting?: number
  trace?: TraceEntry
  /** A trace is showing and this step was not on the contact's path. */
  dimmed?: boolean
  selected?: boolean
  /** When this card's trace mark appears, so a shown path lights up in order. */
  delay?: number
}
