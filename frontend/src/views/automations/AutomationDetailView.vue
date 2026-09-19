<script setup lang="ts">
/**
 * The automation builder (plan 08).
 *
 * An automation is drawn as the path a customer walks: what starts it at the
 * top, then each step as a sentence, splitting into Yes and No wherever it
 * asks a question and pausing wherever it waits. People build it by pressing
 * + on the path and answering the selected card's questions on the right —
 * never by wiring boxes together.
 *
 * Two saving modes, because the stakes differ. A rule that is off is a draft:
 * it saves as you go, unfinished steps and all, and nothing can reach a
 * customer. A rule that is on is live: edits wait until "Save changes", so
 * nobody half-way through changing a message sends the half-changed one.
 */
import { computed, onBeforeUnmount, onMounted, provide, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { useMediaQuery } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger
} from '@/components/ui/dropdown-menu'
import { ErrorState, DeleteConfirmDialog, UnsavedChangesDialog } from '@/components/shared'
import {
  automationsService,
  type Automation, type AutomationCatalog, type AutomationRun, type FilterNode, type AutomationRunPolicy
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { unwrapResponse } from '@/lib/api-utils'
import { toast } from 'vue-sonner'
import {
  ArrowLeft, FlaskConical, MoreHorizontal, Undo2, Redo2, Power, Copy, Trash2, Loader2, X, Route, ChevronRight
} from 'lucide-vue-next'
import AutomationCanvas from '@/components/automations/canvas/AutomationCanvas.vue'
import { flowContextKey, type TraceEntry } from '@/components/automations/canvas/context'
import TriggerInspector from '@/components/automations/inspector/TriggerInspector.vue'
import StepInspector from '@/components/automations/inspector/StepInspector.vue'
import RuleOverview, { type ProblemItem } from '@/components/automations/inspector/RuleOverview.vue'
import TestPanel from '@/components/automations/inspector/TestPanel.vue'
import RunHistory from '@/components/automations/RunHistory.vue'
import {
  CONDITION, WAIT, allIds, copy, countSteps, depthOf, duplicateStep, findStep, insertStep, moveStep,
  neighbours, newStep, removeStep, updateStep, type FlowStep, type InsertPoint
} from '@/components/automations/flow/tree'
import { newAction, missingFields, fieldsFor } from '@/components/crmactions/schema'
import { ruleProse, stepSentence } from '@/components/automations/flow/sentences'
import { missingTriggerFields, triggerDef } from '@/components/automations/flow/triggers'
import { useLookups } from '@/components/automations/useLookups'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const lookups = useLookups()

const canWrite = computed(() => authStore.hasPermission('automations', 'write'))
const canDelete = computed(() => authStore.hasPermission('automations', 'delete'))

// --- The rule, and the draft being edited ---

interface Draft {
  name: string
  description: string
  trigger_type: string
  trigger_config: Record<string, any>
  contact_filter: FilterNode
  steps: FlowStep[]
  run_policy: AutomationRunPolicy
}

const rule = ref<Automation | null>(null)
const catalog = ref<AutomationCatalog | null>(null)
const isLoading = ref(true)
const fetchError = ref(false)

const draft = reactive<Draft>({
  name: '', description: '', trigger_type: '', trigger_config: {},
  contact_filter: { op: 'and', rules: [] }, steps: [],
  run_policy: { once_per_contact: false, cooldown_minutes: 0, max_runs_per_hour: 500 }
})

function draftFrom(a: Automation): Draft {
  return {
    name: a.name,
    description: a.description || '',
    trigger_type: a.trigger_type,
    trigger_config: copy(a.trigger_config || {}),
    contact_filter: copy(a.contact_filter || { op: 'and', rules: [] }),
    steps: copy((a.actions || []) as FlowStep[]),
    run_policy: { ...a.run_policy }
  }
}

function snapshot(d: Draft): string {
  return JSON.stringify(requestBody(d))
}

/** The request body a draft saves as. */
function requestBody(d: Draft = draft): Partial<Automation> {
  return {
    name: d.name.trim() || t('automations.untitled'),
    description: d.description,
    trigger_type: d.trigger_type,
    trigger_config: d.trigger_config,
    contact_filter: d.contact_filter.rules?.length ? d.contact_filter : null,
    actions: d.steps as any,
    run_policy: d.run_policy
  }
}

const savedSnapshot = ref('')
const dirty = computed(() => !!rule.value && snapshot(draft) !== savedSnapshot.value)
const isLive = computed(() => !!rule.value?.enabled)

function adopt(a: Automation, keepDraft = false) {
  rule.value = a
  if (!keepDraft) Object.assign(draft, draftFrom(a))
  savedSnapshot.value = snapshot(draftFrom(a))
}

async function load() {
  try {
    const [ruleResult, catalogResult] = await Promise.all([
      automationsService.get(String(route.params.id)),
      automationsService.catalog()
    ])
    adopt(unwrapResponse<{ automation: Automation }>(ruleResult).automation)
    catalog.value = unwrapResponse<AutomationCatalog>(catalogResult)
    fetchError.value = false
    resetHistory()
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }
  // Everything a card might name, so sentences read "the Billing queue"
  // rather than an id. Loaded after the page shows; cards fill in as it lands.
  lookups.ensure('tags', 'users', 'teams', 'templates', 'pipelines', 'taskTypes', 'fields', 'variables',
    'filterFields', 'flows', 'campaigns', 'accounts')
}

// --- Saving ---

type SaveState = 'idle' | 'saving' | 'saved' | 'error'
const saveState = ref<SaveState>('idle')
let saveTimer: ReturnType<typeof setTimeout> | undefined
let savingPromise: Promise<boolean> | null = null

async function persist(): Promise<boolean> {
  if (!rule.value || !canWrite.value) return false
  const body = requestBody()
  const sent = JSON.stringify(body)
  saveState.value = 'saving'
  const attempt = (async () => {
    try {
      const response = await automationsService.update(rule.value!.id, body)
      const saved = unwrapResponse<{ automation: Automation }>(response).automation
      rule.value = saved
      // What was sent is what is saved; anything typed since stays unsaved.
      savedSnapshot.value = sent
      saveState.value = 'saved'
      return true
    } catch (error: any) {
      saveState.value = 'error'
      reportError(error)
      return false
    } finally {
      savingPromise = null
    }
  })()
  savingPromise = attempt
  return attempt
}

/** A problem the server pinned to a step: open that step and say what it is. */
function reportError(error: any) {
  const message = error?.response?.data?.message || t('common.error')
  const stepId = error?.response?.data?.data?.step_id
  if (stepId) select(stepId === 'trigger' || stepId === 'filter' ? 'start' : stepId, true)
  toast.error(message)
}

// Drafts save as you go.
watch(draft, () => {
  if (!rule.value || isLive.value || !dirty.value || !canWrite.value) return
  clearTimeout(saveTimer)
  saveTimer = setTimeout(() => { persist() }, 700)
}, { deep: true })

async function saveChanges() {
  clearTimeout(saveTimer)
  if (await persist()) toast.success(t('automations.builder.changesLive'))
}

function discardChanges() {
  if (rule.value) Object.assign(draft, draftFrom(rule.value))
  clearTrace()
}

const turningOn = ref(false)
async function turnOn() {
  if (!rule.value) return
  if (problemList.value.length) {
    const first = problemList.value[0]
    select(first.id, true)
    toast.error(t('automations.builder.finishFirst', { n: problemList.value.length }, problemList.value.length))
    return
  }
  turningOn.value = true
  try {
    clearTimeout(saveTimer)
    if (savingPromise) await savingPromise
    if (dirty.value && !(await persist())) return
    const response = await automationsService.enable(rule.value.id)
    adopt(unwrapResponse<{ automation: Automation }>(response).automation, true)
    toast.success(t('automations.builder.nowOn'))
  } catch (error: any) {
    reportError(error)
  } finally {
    turningOn.value = false
  }
}

async function turnOff() {
  if (!rule.value) return
  try {
    const response = await automationsService.disable(rule.value.id)
    const off = unwrapResponse<{ automation: Automation }>(response).automation
    // Unsaved live edits become the draft's edits, and start saving.
    adopt(off, true)
    if (dirty.value) persist()
    toast.success(t('automations.builder.nowOff'))
  } catch (error: any) {
    reportError(error)
  }
}

async function duplicateRule() {
  if (!rule.value) return
  try {
    const response = await automationsService.create({
      ...requestBody(),
      name: t('automations.copyOf', { name: draft.name }),
      enabled: false
    })
    const copy = unwrapResponse<{ automation: Automation }>(response).automation
    router.push(`/automations/${copy.id}`)
  } catch (error: any) {
    reportError(error)
  }
}

const showDelete = ref(false)
async function deleteRule() {
  if (!rule.value) return
  try {
    await automationsService.delete(rule.value.id)
    savedSnapshot.value = snapshot(draft)
    toast.success(t('common.deletedSuccess'))
    router.push('/automations')
  } catch (error: any) {
    reportError(error)
  }
}

// --- Undo ---

const past = ref<string[]>([])
const future = ref<string[]>([])
let lastState = ''
let restoring = false
let historyTimer: ReturnType<typeof setTimeout> | undefined

function editState(): string {
  return JSON.stringify({
    trigger_type: draft.trigger_type, trigger_config: draft.trigger_config,
    contact_filter: draft.contact_filter, steps: draft.steps, run_policy: draft.run_policy
  })
}

function resetHistory() {
  past.value = []
  future.value = []
  lastState = editState()
}

watch(() => editState(), now => {
  if (restoring) return
  clearTimeout(historyTimer)
  // Typing a message is one edit, not forty.
  historyTimer = setTimeout(() => {
    if (now === lastState) return
    past.value.push(lastState)
    if (past.value.length > 60) past.value.shift()
    future.value = []
    lastState = now
  }, 350)
})

function restore(state: string) {
  restoring = true
  Object.assign(draft, JSON.parse(state))
  lastState = state
  if (selectedId.value && selectedId.value !== 'start' && !allIds(draft.steps).includes(selectedId.value)) {
    selectedId.value = null
  }
  setTimeout(() => { restoring = false })
}

function undo() {
  clearTimeout(historyTimer)
  const current = editState()
  if (current !== lastState) past.value.push(lastState)
  const previous = past.value.pop()
  if (previous === undefined) return
  future.value.push(current)
  restore(previous)
}

function redo() {
  const next = future.value.pop()
  if (next === undefined) return
  past.value.push(editState())
  restore(next)
}

// --- Selection and editing the path ---

const selectedId = ref<string | null>(null)
const tab = ref<'build' | 'history'>('build')
const panel = ref<'inspect' | 'test'>('inspect')
const canvasRef = ref<InstanceType<typeof AutomationCanvas> | null>(null)
const sheetOpen = ref(false)
/** On a desktop the inspector is a column; below that, a sheet over the canvas. */
const isDesktop = useMediaQuery('(min-width: 1024px)')

function select(id: string | null, reveal = false) {
  selectedId.value = id
  panel.value = 'inspect'
  sheetOpen.value = !!id
  if (id && reveal) {
    tab.value = 'build'
    setTimeout(() => canvasRef.value?.reveal(id), 50)
  }
}

const selectedStep = computed(() =>
  selectedId.value && selectedId.value !== 'start' ? findStep(draft.steps, selectedId.value) : null)

const limits = computed(() => catalog.value?.limits)
const availableActions = computed(() => catalog.value?.actions || [])

function blockedReason(point: InsertPoint, type: string): string | null {
  const max = limits.value?.max_steps_per_rule ?? 40
  if (countSteps(draft.steps) >= max) return t('automations.blocked.limit', { n: max })
  if (type === CONDITION) {
    const depth = point.parentId === null ? 0 : (depthOf(draft.steps, point.parentId) ?? 0) + 1
    if (depth >= (limits.value?.max_nesting ?? 3)) return t('automations.blocked.nesting')
  }
  return null
}

function insert(point: InsertPoint, type: string) {
  const step: FlowStep = type === CONDITION || type === WAIT ? newStep(type) : newAction(type)
  draft.steps = insertStep(draft.steps, point, step)
  clearTrace()
  select(step.id)
}

function remove(id: string) {
  const before = editState()
  const removed = findStep(draft.steps, id)
  draft.steps = removeStep(draft.steps, id)
  if (selectedId.value === id) select(null)
  clearTrace()
  if (removed) {
    toast(t('automations.builder.removed', { step: t(`automations.steps.${removed.type}.title`, removed.type) }), {
      action: {
        label: t('automations.builder.undo'),
        onClick: () => restore(before)
      }
    })
  }
}

function duplicate(id: string) {
  const { steps, copyId } = duplicateStep(draft.steps, id)
  draft.steps = steps
  if (copyId) select(copyId)
}

function move(id: string, direction: -1 | 1) {
  draft.steps = moveStep(draft.steps, id, direction)
}

function patchStep(id: string, patch: Partial<FlowStep>) {
  draft.steps = updateStep(draft.steps, id, patch)
}

provide(flowContextKey, {
  editable: computed(() => canWrite.value && tab.value === 'build'),
  available: availableActions,
  blockedReason,
  insert,
  remove,
  duplicate,
  select: (id: string | null) => select(id),
  deselect: () => { selectedId.value = null }
})

// --- What is still missing ---

function fieldLabels(type: string, keys: string[]): string {
  return keys.map(key => {
    const field = fieldsFor(type).find(f => f.key === key)
    return field ? t(`automations.fields.${field.label}`).toLowerCase() : key
  }).join(', ')
}

/** Per card: what it still needs. Instant, from the same rules the server checks. */
const problems = computed<Record<string, string>>(() => {
  const out: Record<string, string> = {}
  const missingTrigger = missingTriggerFields(draft.trigger_type, draft.trigger_config)
  if (missingTrigger.length) {
    const def = triggerDef(draft.trigger_type)
    out.trigger = t('automations.problems.fill', {
      fields: missingTrigger.map(key => {
        const f = def?.fields.find(x => x.key === key)
        return f ? t(`automations.triggerFields.${f.label}`).toLowerCase() : key
      }).join(', ')
    })
  }
  const walk = (steps: FlowStep[]) => {
    for (const step of steps) {
      if (step.type === CONDITION) {
        if (!step.config?.filter?.rules?.length) out[step.id] = t('automations.problems.question')
        walk(step.then || [])
        walk(step.else || [])
      } else if (step.type === WAIT) {
        if (!(Number(step.config?.for?.amount) > 0)) out[step.id] = t('automations.problems.wait')
      } else {
        const missing = missingFields(step.type, step.config || {})
        if (missing.length) out[step.id] = t('automations.problems.fill', { fields: fieldLabels(step.type, missing) })
      }
    }
  }
  walk(draft.steps)
  // The server's word on anything the instant check cannot see — a template
  // that lost its approval, a field that was archived. Only for the saved
  // state: once the draft has moved on, the old answer may no longer apply.
  if (!dirty.value) {
    for (const p of rule.value?.problems || []) {
      const key = p.step_id === 'filter' ? 'filter' : p.step_id || ''
      if (key && !out[key]) out[key] = p.message
    }
  }
  return out
})

const problemList = computed<ProblemItem[]>(() => {
  const list: ProblemItem[] = []
  if (!countSteps(draft.steps)) {
    list.push({ id: 'start', title: t('automations.problems.noStepsTitle'), message: t('automations.problems.noSteps') })
  }
  for (const [id, message] of Object.entries(problems.value)) {
    if (id === 'trigger' || id === 'filter') {
      list.push({ id: 'start', title: t(`automations.triggers.${draft.trigger_type}`, draft.trigger_type), message })
      continue
    }
    const step = findStep(draft.steps, id)
    if (step) list.push({ id, title: stepSentence(t, lookups, lookups.state.filterFields, step).title, message })
  }
  return list
})

// --- The rule, written out ---

const prose = computed(() => ruleProse(
  t, lookups, lookups.state.filterFields,
  { type: draft.trigger_type, config: draft.trigger_config },
  draft.contact_filter,
  draft.steps
))

const waitingTotal = computed(() => Object.values(rule.value?.waiting || {}).reduce((a, b) => a + b, 0))

// --- Trying it on a contact, and showing a run's path ---

const trace = ref<Record<string, TraceEntry> | null>(null)
const traceWho = ref('')
/** A test says what would happen; a run from History says what did. */
const traceKind = ref<'test' | 'run'>('test')

function traceFrom(run: AutomationRun): Record<string, TraceEntry> {
  const out: Record<string, TraceEntry> = {}
  for (const r of run.action_results?.list || []) {
    out[r.id] = {
      status: r.status,
      branch: r.output?.branch,
      error: r.error,
      until: r.output?.until,
      dryRun: !!r.output?.dry_run
    }
  }
  return out
}

function onTestResult(run: AutomationRun | null, name: string) {
  if (!run) return clearTrace()
  trace.value = traceFrom(run)
  traceWho.value = name || run.contact_name || ''
  traceKind.value = 'test'
}

function showRun(run: AutomationRun) {
  trace.value = traceFrom(run)
  traceWho.value = run.contact_name || t('automations.history.noContact')
  traceKind.value = 'run'
  tab.value = 'build'
  selectedId.value = null
}

function clearTrace() {
  trace.value = null
  traceWho.value = ''
}

function openTest() {
  tab.value = 'build'
  panel.value = panel.value === 'test' ? 'inspect' : 'test'
  sheetOpen.value = panel.value === 'test'
}

// --- Status ---

const status = computed(() => {
  if (!rule.value) return { label: '', tone: 'neutral' as const }
  if (rule.value.enabled) {
    const failures = rule.value.stats?.failures_24h
    return failures
      ? { label: t('automations.builder.statusOnFailing', { n: failures }, failures), tone: 'bad' as const }
      : { label: t('automations.builder.statusOn'), tone: 'good' as const }
  }
  if (problemList.value.length) {
    return { label: t('automations.builder.statusDraft', { n: problemList.value.length }, problemList.value.length), tone: 'warn' as const }
  }
  return { label: t('automations.builder.statusReady'), tone: 'neutral' as const }
})

const STATUS_PILL: Record<string, string> = {
  good: 'border-emerald-500/25 bg-emerald-500/[0.08] text-emerald-200 light:border-emerald-200 light:bg-emerald-50 light:text-emerald-800',
  warn: 'border-amber-500/30 bg-amber-500/[0.08] text-amber-200 hover:border-amber-500/50 hover:bg-amber-500/[0.14] light:border-amber-200 light:bg-amber-50 light:text-amber-900 light:hover:border-amber-300 light:hover:bg-amber-100',
  bad: 'border-red-500/30 bg-red-500/[0.08] text-red-200 hover:border-red-500/50 hover:bg-red-500/[0.14] light:border-red-200 light:bg-red-50 light:text-red-800 light:hover:border-red-300 light:hover:bg-red-100',
  neutral: 'border-white/10 bg-white/[0.04] text-white/70 light:border-gray-200 light:bg-gray-50 light:text-gray-600'
}
const STATUS_DOT: Record<string, string> = {
  good: 'bg-emerald-400 light:bg-emerald-600',
  warn: 'bg-amber-400 light:bg-amber-500',
  bad: 'bg-red-400 light:bg-red-600',
  neutral: 'bg-white/35 light:bg-gray-400'
}

/**
 * The status is also the way to what it is about: a draft's opens the first
 * thing left to finish, a failing rule's opens its history.
 */
const statusAction = computed<(() => void) | null>(() => {
  if (status.value.tone === 'warn' && problemList.value.length) return () => select(problemList.value[0].id, true)
  if (status.value.tone === 'bad') return () => { tab.value = 'history' }
  return null
})

const saveLabel = computed(() => {
  if (isLive.value) return dirty.value ? t('automations.builder.unsaved') : ''
  switch (saveState.value) {
    case 'saving': return t('automations.builder.saving')
    case 'saved': return dirty.value ? '' : t('automations.builder.draftSaved')
    case 'error': return t('automations.builder.saveFailed')
  }
  return ''
})

// --- Keyboard ---

function typing(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el) return false
  return el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName) || !!el.closest('[role="dialog"]')
}

function onKeydown(event: KeyboardEvent) {
  if (tab.value !== 'build') return
  const mod = event.metaKey || event.ctrlKey
  if (event.key === 'Escape' && !typing(event.target)) {
    if (trace.value) clearTrace()
    else select(null)
    return
  }
  if (typing(event.target) || !canWrite.value) return
  if (mod && event.key.toLowerCase() === 'z') {
    event.preventDefault()
    if (event.shiftKey) redo()
    else undo()
  } else if (mod && event.key.toLowerCase() === 'y') {
    event.preventDefault()
    redo()
  } else if ((event.key === 'Delete' || event.key === 'Backspace') && selectedStep.value) {
    event.preventDefault()
    remove(selectedStep.value.id)
  }
}

// --- Leaving ---

const showLeave = ref(false)
let pendingLeave: (() => void) | null = null

onBeforeRouteLeave(async (_to, _from, next) => {
  clearTimeout(saveTimer)
  if (!isLive.value && dirty.value && canWrite.value) {
    // A draft is never lost: finish saving it on the way out.
    await persist()
    return next()
  }
  if (isLive.value && dirty.value) {
    showLeave.value = true
    pendingLeave = () => next()
    return next(false)
  }
  next()
})

function leave() {
  showLeave.value = false
  savedSnapshot.value = snapshot(draft)
  pendingLeave?.()
}

function beforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value) {
    event.preventDefault()
    event.returnValue = ''
  }
}

onMounted(() => {
  load()
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('beforeunload', beforeUnload)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('beforeunload', beforeUnload)
  clearTimeout(saveTimer)
  clearTimeout(historyTimer)
})

watch(() => route.params.id, (id, before) => {
  if (id && id !== before) {
    isLoading.value = true
    selectedId.value = null
    clearTrace()
    load()
  }
})

const selectedNeighbours = computed(() => selectedStep.value ? neighbours(draft.steps, selectedStep.value.id) : { canUp: false, canDown: false })
const serverProblemFor = (id: string) => (dirty.value ? undefined : rule.value?.problems?.find(p => p.step_id === id)?.message)
</script>

<template>
  <div class="flex h-full flex-col bg-background">
    <ErrorState v-if="fetchError" :message="t('automations.loadFailed')" @retry="load" />
    <div v-else-if="isLoading" class="flex h-full items-center justify-center text-muted-foreground">
      <Loader2 class="h-5 w-5 animate-spin" />
    </div>

    <template v-else-if="rule">
      <!-- The bar: which rule, whether it is live, and the one thing to do next. -->
      <header class="border-b border-white/[0.08] bg-[#0a0a0b]/95 light:border-gray-200 light:bg-white/95">
        <div class="flex min-h-14 flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2 sm:px-5">
          <Button variant="ghost" size="icon" class="-ml-1.5 h-9 w-9 shrink-0" :aria-label="t('common.back')" @click="router.push('/automations')">
            <ArrowLeft class="h-4 w-4" />
          </Button>
          <div class="flex min-w-0 flex-1 basis-56 items-center gap-3">
            <input
              v-model="draft.name"
              :disabled="!canWrite"
              :aria-label="t('automations.builder.name')"
              :placeholder="t('automations.untitled')"
              class="min-w-0 max-w-[28rem] flex-1 truncate rounded-sm border border-transparent bg-transparent px-1.5 py-1 text-lg font-semibold text-white outline-none transition-colors hover:border-white/10 focus:border-emerald-500/60 focus:ring-2 focus:ring-emerald-500/20 light:text-gray-900 light:hover:border-gray-200"
            >
            <component
              :is="statusAction ? 'button' : 'span'"
              v-if="status.label"
              :type="statusAction ? 'button' : undefined"
              :class="[
                'inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border py-1 pl-2.5 text-xs font-medium transition-colors duration-150',
                statusAction ? 'pr-1.5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/40' : 'pr-2.5',
                STATUS_PILL[status.tone]
              ]"
              :title="statusAction && status.tone === 'warn' ? t('automations.builder.fixNext') : undefined"
              @click="statusAction?.()"
            >
              <span :class="['h-1.5 w-1.5 shrink-0 rounded-full', STATUS_DOT[status.tone]]" aria-hidden="true" />
              {{ status.label }}
              <ChevronRight v-if="statusAction" class="h-3.5 w-3.5 opacity-70" aria-hidden="true" />
            </component>
            <span v-if="saveLabel" class="hidden shrink-0 text-xs text-muted-foreground md:inline" aria-live="polite">{{ saveLabel }}</span>
          </div>
          <div class="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              :class="panel === 'test' && tab === 'build' ? 'border-emerald-500/60 text-emerald-300 light:text-emerald-700' : ''"
              @click="openTest"
            >
              <FlaskConical class="mr-1.5 h-4 w-4" />
              <span class="max-sm:hidden">{{ t('automations.builder.tryIt') }}</span>
              <span class="sm:hidden">{{ t('automations.builder.test') }}</span>
            </Button>
            <template v-if="canWrite">
              <template v-if="isLive">
                <Button v-if="dirty" size="sm" :disabled="saveState === 'saving'" @click="saveChanges">
                  <Loader2 v-if="saveState === 'saving'" class="mr-1.5 h-4 w-4 animate-spin" />
                  {{ t('automations.builder.saveChanges') }}
                </Button>
              </template>
              <Button v-else size="sm" :disabled="turningOn" @click="turnOn">
                <Loader2 v-if="turningOn" class="mr-1.5 h-4 w-4 animate-spin" />
                <Power v-else class="mr-1.5 h-4 w-4" />
                {{ t('automations.builder.turnOn') }}
              </Button>
            </template>
            <DropdownMenu v-if="canWrite">
              <DropdownMenuTrigger as-child>
                <Button variant="ghost" size="icon" class="h-9 w-9" :aria-label="t('common.actions')">
                  <MoreHorizontal class="h-4 w-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" class="w-48">
                <DropdownMenuItem v-if="isLive" @click="turnOff">
                  <Power class="mr-2 h-4 w-4" />{{ t('automations.builder.turnOff') }}
                </DropdownMenuItem>
                <DropdownMenuItem @click="duplicateRule">
                  <Copy class="mr-2 h-4 w-4" />{{ t('automations.duplicate') }}
                </DropdownMenuItem>
                <template v-if="canDelete">
                  <DropdownMenuSeparator />
                  <DropdownMenuItem class="text-destructive focus:text-destructive" @click="showDelete = true">
                    <Trash2 class="mr-2 h-4 w-4" />{{ t('common.delete') }}
                  </DropdownMenuItem>
                </template>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>
        <div class="flex items-center gap-1 px-3 sm:px-4">
          <nav class="flex" role="tablist" :aria-label="t('automations.builder.views')">
            <button
              v-for="v in (['build', 'history'] as const)"
              :key="v"
              type="button"
              role="tab"
              :aria-selected="tab === v"
              :class="[
                '-mb-px border-b-2 px-3 pb-2 pt-1 text-sm transition-colors',
                tab === v
                  ? 'border-emerald-500 font-medium text-white light:text-gray-900'
                  : 'border-transparent text-white/55 hover:text-white light:text-gray-500 light:hover:text-gray-900'
              ]"
              @click="tab = v"
            >{{ t(`automations.builder.tab.${v}`) }}</button>
          </nav>
          <div v-if="tab === 'build' && canWrite" class="ml-auto flex items-center gap-0.5 pb-1">
            <Button
              variant="ghost" size="icon" class="h-8 w-8" :disabled="!past.length"
              :aria-label="t('automations.builder.undo')" :title="`${t('automations.builder.undo')} (⌘Z)`"
              @click="undo"
            >
              <Undo2 class="h-4 w-4" />
            </Button>
            <Button
              variant="ghost" size="icon" class="h-8 w-8" :disabled="!future.length"
              :aria-label="t('automations.builder.redo')" :title="`${t('automations.builder.redo')} (⇧⌘Z)`"
              @click="redo"
            >
              <Redo2 class="h-4 w-4" />
            </Button>
          </div>
        </div>
      </header>

      <!-- Build: the path on the left, the selected card's questions on the right. -->
      <div v-show="tab === 'build'" class="flex min-h-0 flex-1">
        <section class="relative min-w-0 flex-1">
          <div
            v-if="isLive && dirty"
            class="absolute inset-x-0 top-0 z-10 flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-amber-500/25 bg-amber-950/70 px-4 py-2 text-sm text-amber-100 backdrop-blur light:border-amber-200 light:bg-amber-50/95 light:text-amber-900"
            role="status"
          >
            <span class="min-w-0 flex-1">{{ t('automations.builder.liveEditing') }}</span>
            <Button variant="ghost" size="sm" class="h-7" @click="discardChanges">{{ t('automations.builder.discard') }}</Button>
            <Button size="sm" class="h-7" :disabled="saveState === 'saving'" @click="saveChanges">{{ t('automations.builder.saveChanges') }}</Button>
          </div>
          <div
            v-else-if="trace"
            class="absolute inset-x-0 top-0 z-10 flex items-center gap-3 border-b border-emerald-500/20 bg-emerald-950/70 px-4 py-2 text-sm text-emerald-100 backdrop-blur light:border-emerald-200 light:bg-emerald-50/95 light:text-emerald-900"
            role="status"
          >
            <Route class="h-4 w-4 shrink-0" />
            <span class="min-w-0 flex-1 truncate">{{ t(traceKind === 'test' ? 'automations.builder.showingTestPath' : 'automations.builder.showingPath', { name: traceWho }) }}</span>
            <Button variant="ghost" size="sm" class="h-7" @click="clearTrace">
              <X class="mr-1 h-3.5 w-3.5" />{{ t('automations.test.clear') }}
            </Button>
          </div>
          <AutomationCanvas
            ref="canvasRef"
            :steps="draft.steps"
            :trigger-type="draft.trigger_type"
            :trigger-config="draft.trigger_config"
            :filter="draft.contact_filter"
            :selected-id="selectedId"
            :problems="problems"
            :waiting="rule.waiting || {}"
            :trace="trace"
            @select="id => select(id)"
          />
        </section>

        <!-- Desktop inspector. -->
        <aside class="hidden w-[400px] shrink-0 overflow-y-auto border-l border-white/[0.08] bg-card/40 light:border-gray-200 light:bg-white lg:block">
          <TestPanel
            v-if="panel === 'test'"
            :rule-id="rule.id"
            :draft="() => requestBody()"
            :steps="draft.steps"
            :problems="problems"
            @result="onTestResult"
            @select="id => select(id, true)"
          />
          <TriggerInspector
            v-else-if="selectedId === 'start'"
            :type="draft.trigger_type"
            :config="draft.trigger_config"
            :filter="draft.contact_filter"
            :available="(catalog?.triggers || []).map(tr => tr.type)"
            :disabled="!canWrite"
            :server-problem="serverProblemFor('trigger')"
            @update:type="v => draft.trigger_type = v"
            @update:config="v => draft.trigger_config = v"
            @update:filter="v => draft.contact_filter = v"
          />
          <StepInspector
            v-else-if="selectedStep"
            :key="selectedStep.id"
            :step="selectedStep"
            :disabled="!canWrite"
            :server-problem="serverProblemFor(selectedStep.id)"
            :waiting="rule.waiting?.[selectedStep.id]"
            :can-up="selectedNeighbours.canUp"
            :can-down="selectedNeighbours.canDown"
            @update="patch => patchStep(selectedStep!.id, patch)"
            @remove="remove(selectedStep!.id)"
            @duplicate="duplicate(selectedStep!.id)"
            @move="d => move(selectedStep!.id, d)"
          />
          <RuleOverview
            v-else
            :prose="prose"
            :problems="problemList"
            :enabled="rule.enabled"
            :stats="rule.stats"
            :waiting-total="waitingTotal"
            :policy="draft.run_policy"
            :description="draft.description"
            :disabled="!canWrite"
            @select="id => select(id, true)"
            @update:policy="v => draft.run_policy = v"
            @update:description="v => draft.description = v"
          />
        </aside>
      </div>

      <RunHistory v-if="tab === 'history'" :rule-id="rule.id" class="min-h-0 flex-1 overflow-y-auto" @show="showRun" />

      <!-- Small screens: the same questions, in a sheet over the canvas. -->
      <Sheet :open="sheetOpen && !isDesktop" @update:open="v => { sheetOpen = v; if (!v) { selectedId = null; panel = 'inspect' } }">
        <SheetContent side="bottom" class="max-h-[85vh] overflow-y-auto p-0">
          <SheetTitle class="sr-only">{{ t('automations.builder.inspector') }}</SheetTitle>
          <TestPanel
            v-if="panel === 'test'"
            :rule-id="rule.id"
            :draft="() => requestBody()"
            :steps="draft.steps"
            :problems="problems"
            @result="onTestResult"
            @select="id => select(id, true)"
          />
          <TriggerInspector
            v-else-if="selectedId === 'start'"
            :type="draft.trigger_type"
            :config="draft.trigger_config"
            :filter="draft.contact_filter"
            :available="(catalog?.triggers || []).map(tr => tr.type)"
            :disabled="!canWrite"
            :server-problem="serverProblemFor('trigger')"
            @update:type="v => draft.trigger_type = v"
            @update:config="v => draft.trigger_config = v"
            @update:filter="v => draft.contact_filter = v"
          />
          <StepInspector
            v-else-if="selectedStep"
            :key="`sheet-${selectedStep.id}`"
            :step="selectedStep"
            :disabled="!canWrite"
            :server-problem="serverProblemFor(selectedStep.id)"
            :waiting="rule.waiting?.[selectedStep.id]"
            :can-up="selectedNeighbours.canUp"
            :can-down="selectedNeighbours.canDown"
            @update="patch => patchStep(selectedStep!.id, patch)"
            @remove="remove(selectedStep!.id)"
            @duplicate="duplicate(selectedStep!.id)"
            @move="d => move(selectedStep!.id, d)"
          />
        </SheetContent>
      </Sheet>
    </template>

    <DeleteConfirmDialog
      v-model:open="showDelete"
      :title="t('automations.builder.deleteTitle')"
      :description="t('automations.builder.deleteDescription', { name: draft.name })"
      :confirm-label="t('common.delete')"
      @confirm="deleteRule"
    />
    <UnsavedChangesDialog :open="showLeave" @stay="showLeave = false" @leave="leave" />
  </div>
</template>
