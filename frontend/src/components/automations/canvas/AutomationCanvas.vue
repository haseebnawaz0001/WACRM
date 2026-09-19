<script setup lang="ts">
/**
 * The automation, drawn as the path a customer walks.
 *
 * Everything on it is derived: the layout from the step tree, each card's
 * words from the sentences, its state from the rule's problems, the contacts
 * waiting in it, and — when somebody has tried the rule on a contact — the
 * path that contact took. Nothing is dragged or wired by hand.
 */
import { computed, markRaw, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { VueFlow, useVueFlow } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { Controls } from '@vue-flow/controls'
import type { FilterNode } from '@/services/api'
import { CARD_WIDTH, layoutFlow } from '../flow/layout'
import { lineCount } from '../flow/measure'
import { WAIT, findStep, type FlowStep } from '../flow/tree'
import { SLOT, around, filterParts, lowerFirst, stepSentence, triggerSentence, type Part, type Sentence } from '../flow/sentences'
import { triggerDef } from '../flow/triggers'
import { useLookups } from '../useLookups'
import type { NodeView, TraceEntry } from './context'
import FlowStepNode from './FlowStepNode.vue'
import FlowStartNode from './FlowStartNode.vue'
import FlowEndNode from './FlowEndNode.vue'
import FlowJoinNode from './FlowJoinNode.vue'
import FlowEmptyNode from './FlowEmptyNode.vue'
import FlowEdge from './FlowEdge.vue'

const props = defineProps<{
  steps: FlowStep[]
  triggerType: string
  triggerConfig: Record<string, any>
  filter?: FilterNode | null
  selectedId: string | null
  /** Step id (or 'trigger' / 'filter') → what it still needs. */
  problems: Record<string, string>
  waiting: Record<string, number>
  /** What happened to one contact, step by step; null when not showing one. */
  trace: Record<string, TraceEntry> | null
}>()

const emit = defineEmits<{ select: [id: string | null] }>()

const { t } = useI18n()
const lookups = useLookups()

const flowId = `automation-canvas-${Math.random().toString(36).slice(2, 8)}`
const { fitView, setViewport, getViewport, dimensions, findNode } = useVueFlow(flowId)

// Vue Flow types node components by its own props shape; ours take a
// narrower data prop, so the maps are cast rather than every node widened.
const nodeTypes: any = {
  start: markRaw(FlowStartNode),
  action: markRaw(FlowStepNode),
  condition: markRaw(FlowStepNode),
  wait: markRaw(FlowStepNode),
  end: markRaw(FlowEndNode),
  join: markRaw(FlowJoinNode),
  empty: markRaw(FlowEmptyNode)
}
const edgeTypes: any = { flow: markRaw(FlowEdge) }

const fields = computed(() => lookups.state.filterFields)

/** Every step's words, computed once for both the layout and the cards. */
const sentences = computed(() => {
  const map = new Map<string, Sentence>()
  const walk = (list: FlowStep[]) => {
    for (const step of list) {
      map.set(step.id, stepSentence(t, lookups, fields.value, step))
      walk(step.then || [])
      walk(step.else || [])
    }
  }
  walk(props.steps)
  return map
})

const start = computed(() => {
  const sentence = triggerSentence(t, lookups, props.triggerType, props.triggerConfig)
  const who = filterParts(t, lookups, fields.value, props.filter)
  return {
    title: t('automations.overview.when', { what: lowerFirst(sentence.title) }),
    sentence,
    audience: who.length ? around(t('automations.canvas.onlyFor', { who: SLOT }), who) : [] as Part[],
    problem: props.problems.trigger || props.problems.filter
  }
})

// Cards are as tall as their words. Every size here mirrors a class on the
// card, and the app's rem is 14px, not 16 — so sizes are taken from the root
// font rather than assumed. Fonts arrive after the first draw, so the heights
// are measured again once they have.
const rem = typeof document === 'undefined' ? 16 : Number.parseFloat(getComputedStyle(document.documentElement).fontSize) || 16
const DETAIL = { size: 0.75 * rem, line: rem }          // text-xs leading-4
const TITLE = { size: 13.5, line: 1.25 * rem }          // text-[13.5px] leading-5
const PAD_Y = 0.75 * rem                                // py-3 / pt-3
// Card less its border, px-3.5 either side, the w-8 tile and gap-3.
const TEXT_WIDTH = CARD_WIDTH - 2 - 2 * 0.875 * rem - 2 * rem - 0.75 * rem
const PROBLEM_WIDTH = TEXT_WIDTH - rem                  // the warning icon and its margin
const fontsReady = ref(0)
onMounted(() => { document.fonts?.ready.then(() => { fontsReady.value++ }) })

function detailLines(problem: string | undefined, detail: string | undefined): number {
  return problem
    ? lineCount(problem, DETAIL.size, 400, PROBLEM_WIDTH, 2)
    : lineCount(detail || '', DETAIL.size, 500, TEXT_WIDTH, 2)
}

function stepHeight(step: FlowStep): number {
  void fontsReady.value
  const lines = detailLines(props.problems[step.id], sentences.value.get(step.id)?.detail)
  const text = TITLE.line + (lines ? 0.125 * rem + lines * DETAIL.line : 0)
  const tile = 0.125 * rem + 2 * rem // mt-0.5 h-8
  return Math.ceil(2 + PAD_Y * 2 + Math.max(tile, text))
}

const startHeight = computed(() => {
  void fontsReady.value
  const s = start.value
  const titleLines = Math.max(1, lineCount(s.title, TITLE.size, 500, TEXT_WIDTH, 2))
  const lines = detailLines(s.problem, s.sentence.detail)
  const head = titleLines * TITLE.line + (lines ? 0.125 * rem + lines * DETAIL.line : 0)
  // pt-3, the heading, mt-2.5, the hairline, pt-2, the audience line, and room below.
  return Math.ceil(2 + PAD_Y + head + 0.625 * rem + 1 + 0.5 * rem + DETAIL.line + PAD_Y)
})

const laidOut = computed(() => layoutFlow(props.steps, { start: startHeight.value, step: stepHeight }))

/** Which steps the traced contact reached. */
const reached = computed<Set<string> | null>(() => props.trace ? new Set(Object.keys(props.trace)) : null)

function nodeReached(id: string, data: Record<string, any>): boolean {
  const set = reached.value
  if (!set) return true
  if (id === 'start') return true
  if (id === 'end') {
    // The contact reached the end if nothing stopped or parked them and the
    // last step on the main path was reached.
    const halted = Object.values(props.trace || {}).some(e => e.status === 'failed' || e.status === 'waiting')
    const last = props.steps[props.steps.length - 1]
    return !halted && (!last || set.has(last.id))
  }
  if (id.endsWith('__join')) return set.has(data.question)
  if (id.endsWith('__empty')) {
    const entry = props.trace?.[data.question]
    return !!entry && entry.branch === data.branch
  }
  return set.has(id)
}

/** Bumped whenever a different path is shown, so it lights up afresh. */
const traceKey = ref(0)
watch(() => props.trace, now => {
  traceKey.value++
  if (now) nextTick(frameTrace)
})

/**
 * Frames a shown path: the whole rule when it fits at a readable size, so
 * the faded branch the contact did not take is seen beside the one they did;
 * otherwise just the path they walked. Either way with room for the banner.
 */
function frameTrace() {
  const { width: vw, height: vh } = dimensions.value
  if (!vw || !vh) return
  const padding = 0.14
  const minZoom = vw < 640 ? 0.75 : 0.6
  const { width, height } = laidOut.value
  const fits = Math.min((vw * (1 - padding * 2)) / width, (vh * (1 - padding * 2)) / height) >= minZoom
  const nodes = fits ? undefined : laidOut.value.nodes.filter(n => nodeReached(n.id, n.data as any)).map(n => n.id)
  fitView({ nodes, padding, maxZoom: 1, minZoom, duration: 300 })
}

/** Paths light up one stretch at a time, in the order the contact walked them. */
const STAGGER = 140

const edges = computed(() => {
  let rank = 0
  return laidOut.value.edges.map(edge => {
    if (!props.trace) return edge
    const sourceNode = laidOut.value.nodes.find(n => n.id === edge.source)
    const targetNode = laidOut.value.nodes.find(n => n.id === edge.target)
    const sourceOk = sourceNode ? nodeReached(sourceNode.id, sourceNode.data as any) : false
    const targetOk = targetNode ? nodeReached(targetNode.id, targetNode.data as any) : false
    let taken = sourceOk && targetOk
    if (edge.data?.label) {
      const entry = props.trace[edge.source]
      taken = taken && !!entry && entry.branch === (edge.data.label === 'yes' ? 'then' : 'else')
    }
    const delay = taken ? rank++ * STAGGER : undefined
    return { ...edge, data: { ...edge.data, taken, dimmed: !taken, delay, traceKey: traceKey.value } }
  })
})

/** When each card's trace mark appears: just after the stretch into it is drawn. */
const markDelay = computed(() => {
  const out = new Map<string, number>()
  for (const edge of edges.value) {
    const delay = (edge.data as any)?.delay
    if (delay !== undefined && !out.has(edge.target)) out.set(edge.target, delay + 200)
  }
  return out
})

const nodes = computed(() => laidOut.value.nodes.map(node => {
  const data = node.data as Record<string, any>
  const traced = !!props.trace
  const isReached = nodeReached(node.id, data)

  if (node.type === 'start') {
    const s = start.value
    const view: NodeView & { icon?: any; audience?: Part[] } = {
      title: s.title,
      detail: s.sentence.detail,
      parts: s.sentence.parts,
      problem: s.problem,
      selected: props.selectedId === 'start',
      icon: triggerDef(props.triggerType)?.icon,
      audience: s.audience
    }
    return { ...node, data: { view } }
  }

  if (node.type === 'end' || node.type === 'join' || node.type === 'empty') {
    return { ...node, data: { ...data, dimmed: traced && !isReached } }
  }

  const step = data.step as FlowStep
  const sentence = sentences.value.get(step.id) || { title: step.type }
  const view: NodeView = {
    title: sentence.title,
    detail: sentence.detail,
    parts: sentence.parts,
    problem: props.problems[step.id],
    waiting: step.type === WAIT ? props.waiting[step.id] : undefined,
    trace: props.trace?.[step.id],
    dimmed: traced && !isReached,
    selected: props.selectedId === step.id,
    delay: markDelay.value.get(step.id)
  }
  return { ...node, data: { step, view, traceKey: traceKey.value } }
}))

function onNodeClick({ node }: { node: { id: string; type?: string } }) {
  if (node.type === 'end' || node.type === 'join' || node.type === 'empty') return
  emit('select', node.id)
}

/**
 * Opens on the start of the path at full size. Fitting a long rule to the
 * screen shrinks every card below reading size, so a rule that does not fit
 * starts at the top and scrolls — the same way a document does.
 */
function frame() {
  const { width: vw, height: vh } = dimensions.value
  if (!vw || !vh) return
  const { width, height } = laidOut.value
  const padding = 48
  if (height + padding * 2 <= vh && width + padding * 2 <= vw) {
    fitView({ padding: 0.15, maxZoom: 1, duration: 0 })
    return
  }
  // On a phone the cards stay at reading size, with the start card centred,
  // and the path pans under a finger: a branching rule squeezed to 390px
  // would be a picture of a rule nobody can read. A wider screen shrinks to
  // show both branches, but never below what can still be read.
  const phone = vw < 640
  const zoom = phone ? 1 : Math.max(0.6, Math.min(1, (vw - padding * 2) / width))
  setViewport({ x: vw / 2 - (width / 2) * zoom, y: phone ? 20 : 40, zoom }, { duration: 0 })
}

const framed = ref(false)
function onPaneReady() {
  nextTick(() => {
    frame()
    framed.value = true
  })
}

/**
 * Brings a card into view when it is chosen from outside the canvas, moving
 * the path as little as it can: a card already in sight stays where it is,
 * and one off to the side slides in just far enough, at the same zoom, so the
 * cards around it stay where the person last saw them.
 */
function reveal(id: string) {
  const node = findNode(id)
  const { width: vw, height: vh } = dimensions.value
  if (!node || !vw || !vh) return
  const { x, y, zoom } = getViewport()
  const w = (node.dimensions?.width || CARD_WIDTH) * zoom
  const h = (node.dimensions?.height || 64) * zoom
  const left = node.position.x * zoom + x
  const top = node.position.y * zoom + y
  const pad = vw < 640 ? 16 : 48
  const padTop = pad + 44 // clear of a banner across the top
  let dx = 0
  let dy = 0
  if (w > vw - pad * 2) dx = (vw - w) / 2 - left
  else if (left < pad) dx = pad - left
  else if (left + w > vw - pad) dx = vw - pad - (left + w)
  if (top < padTop) dy = padTop - top
  else if (top + h > vh - pad) dy = vh - pad - (top + h)
  if (dx || dy) setViewport({ x: x + dx, y: y + dy, zoom }, { duration: 300 })
}

watch(() => dimensions.value.width, (now, before) => {
  if (framed.value && before === 0 && now > 0) frame()
})

defineExpose({ frame, reveal, findStep: (id: string) => findStep(props.steps, id) })
</script>

<template>
  <div class="automation-canvas relative h-full w-full">
    <VueFlow
      :id="flowId"
      :nodes="nodes"
      :edges="edges"
      :node-types="nodeTypes"
      :edge-types="edgeTypes"
      :nodes-draggable="false"
      :nodes-connectable="false"
      :elements-selectable="false"
      :edges-updatable="false"
      :pan-on-scroll="true"
      :zoom-on-scroll="false"
      :zoom-on-pinch="true"
      :zoom-on-double-click="false"
      :min-zoom="0.25"
      :max-zoom="1.5"
      :delete-key-code="null"
      class="h-full"
      @node-click="onNodeClick"
      @pane-click="emit('select', null)"
      @pane-ready="onPaneReady"
    >
      <Background variant="dots" :gap="22" :size="1.5" />
      <Controls position="bottom-left" :show-interactive="false" @fit-view="frame" />
    </VueFlow>
  </div>
</template>

<style>
@import '@vue-flow/core/dist/style.css';
@import '@vue-flow/core/dist/theme-default.css';
@import '@vue-flow/controls/dist/style.css';

.automation-canvas {
  --flow-edge: rgba(255, 255, 255, 0.18);
  --flow-dots: rgba(255, 255, 255, 0.2);
}
.light .automation-canvas {
  --flow-edge: #d1d5db;
  --flow-dots: rgba(15, 23, 42, 0.26);
}

/* An SVG fill attribute cannot read a CSS variable; a rule can. */
.automation-canvas .vue-flow__background circle {
  fill: var(--flow-dots);
}

/* A shown path draws itself in, one stretch after another. */
.automation-canvas .flow-lit {
  fill: none;
  stroke: hsl(var(--primary));
  stroke-width: 2.25;
  stroke-linecap: round;
  stroke-dasharray: 1;
  stroke-dashoffset: 1;
  animation: flow-draw 240ms cubic-bezier(0.25, 1, 0.5, 1) forwards;
}
@keyframes flow-draw {
  to { stroke-dashoffset: 0; }
}
.automation-canvas .flow-mark {
  animation: flow-mark-in 220ms cubic-bezier(0.25, 1, 0.5, 1) both;
}
@keyframes flow-mark-in {
  from { opacity: 0; transform: translateY(3px); }
}
@media (prefers-reduced-motion: reduce) {
  .automation-canvas .flow-lit {
    animation: none;
    stroke-dashoffset: 0;
  }
  .automation-canvas .flow-mark {
    animation: none;
  }
}

/* Cards are the whole node; Vue Flow's default node chrome is not. */
.automation-canvas .vue-flow__node {
  padding: 0;
  border: 0;
  background: transparent;
  box-shadow: none;
  border-radius: 0;
  cursor: pointer;
}
.automation-canvas .vue-flow__node-join,
.automation-canvas .vue-flow__node-end,
.automation-canvas .vue-flow__node-empty {
  cursor: default;
}

.automation-canvas .vue-flow__controls {
  box-shadow: none;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 6px;
  overflow: hidden;
}
.automation-canvas .vue-flow__controls-button {
  background: hsl(var(--card));
  border-bottom-color: rgba(255, 255, 255, 0.06);
  fill: currentColor;
  color: rgba(255, 255, 255, 0.7);
  width: 28px;
  height: 28px;
}
.automation-canvas .vue-flow__controls-button:hover {
  background: hsl(var(--accent));
  color: #fff;
}
.light .automation-canvas .vue-flow__controls {
  border-color: #e5e7eb;
}
.light .automation-canvas .vue-flow__controls-button {
  border-bottom-color: #f3f4f6;
  color: #4b5563;
}
.light .automation-canvas .vue-flow__controls-button:hover {
  color: #111827;
}
</style>
