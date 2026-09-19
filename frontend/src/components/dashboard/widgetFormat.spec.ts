import { describe, it, expect } from 'vitest'
import { changeTone, formatMinutes, formatValue, splitLabel, valueLabel } from './widgetFormat'

const t = (key: string) => (key === 'dashboard.notSet' ? 'Not set' : key === 'dashboard.values.status.open' ? 'Open' : key)
const te = (key: string) => key === 'dashboard.values.status.open'

describe('formatMinutes', () => {
  it('reads as a person would say it', () => {
    expect(formatMinutes(0.5)).toBe('30s')
    expect(formatMinutes(45)).toBe('45m')
    expect(formatMinutes(80)).toBe('1h 20m')
    expect(formatMinutes(120)).toBe('2h')
    expect(formatMinutes(60 * 24 * 2 + 60 * 4)).toBe('2d 4h')
  })
})

describe('formatValue', () => {
  it('says money as money', () => {
    expect(formatValue(1250, { unit: 'money', currency: 'EUR' })).toMatch(/1,250|1\.250|1 250/)
  })
  it('does not call an average of nothing zero minutes', () => {
    expect(formatValue(0, { unit: 'minutes' })).toBe('—')
    expect(formatValue(90, { unit: 'seconds' })).toBe('2m')
  })
  it('keeps counts plain until they are large', () => {
    expect(formatValue(1234)).toBe((1234).toLocaleString())
    expect(formatValue(12_345)).toBe('12.3K')
  })
})

describe('changeTone', () => {
  it('follows whether a rise is good news', () => {
    expect(changeTone(10)).toBe('good')
    expect(changeTone(-10)).toBe('bad')
    expect(changeTone(10, true)).toBe('bad')
    expect(changeTone(-10, true)).toBe('good')
    expect(changeTone(0, true)).toBe('flat')
  })
})

describe('splitLabel', () => {
  it('puts enums into words and keeps names the server resolved', () => {
    expect(splitLabel(t, te, 'status', { label: 'open', key: 'open' })).toBe('Open')
    expect(splitLabel(t, te, 'owner', { label: 'Amara Okafor', key: '5f0c…' })).toBe('Amara Okafor')
    expect(splitLabel(t, te, 'status', { label: '', key: '' })).toBe('Not set')
  })
  it('makes an unknown value readable rather than raw', () => {
    expect(valueLabel(t, te, 'status', 'partially_failed')).toBe('Partially failed')
  })
})
