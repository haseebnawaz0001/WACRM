import { describe, it, expect } from 'vitest'
import { reactive } from 'vue'
import { duplicateStep, findStep, newStep, updateStep, type FlowStep } from './tree'

// The builder's draft is a reactive object, and an inspector patches a step
// with a config spread from it — so the patch holds reactive proxies at every
// level. structuredClone refuses those, which once dropped every edit to a
// step whose config has nested objects (a follow-up's due date and owner).
function draftWithTask() {
  return reactive({
    steps: [{
      id: 'task',
      type: 'create_task',
      config: { title: 'Call back', due_in: { amount: 1, unit: 'days' }, owner: { mode: 'contact_owner' } }
    }] as FlowStep[]
  })
}

describe('updateStep', () => {
  it('applies a patch built from reactive config', () => {
    const draft = draftWithTask()
    const config = { ...draft.steps[0].config, title: 'Call back today' }

    draft.steps = updateStep(draft.steps, 'task', { config })

    const step = findStep(draft.steps, 'task')!
    expect(step.config.title).toBe('Call back today')
    expect(step.config.due_in).toEqual({ amount: 1, unit: 'days' })
  })

  it('leaves the old tree untouched for undo', () => {
    const draft = draftWithTask()
    const before = draft.steps
    const after = updateStep(before, 'task', { config: { ...before[0].config, title: 'Changed' } })

    expect(before[0].config.title).toBe('Call back')
    expect(after[0].config.title).toBe('Changed')
  })
})

describe('duplicateStep', () => {
  it('copies a reactive step with a fresh id', () => {
    const draft = draftWithTask()
    const { steps, copyId } = duplicateStep(draft.steps, 'task')

    expect(steps).toHaveLength(2)
    expect(copyId).not.toBe('task')
    expect(findStep(steps, copyId!)!.config.owner).toEqual({ mode: 'contact_owner' })
  })
})

describe('newStep', () => {
  it('copies reactive config rather than sharing it', () => {
    const config = reactive({ for: { amount: 2, unit: 'hours' } })
    const step = newStep('wait', config)
    config.for.amount = 5

    expect(step.config.for).toEqual({ amount: 2, unit: 'hours' })
  })
})
