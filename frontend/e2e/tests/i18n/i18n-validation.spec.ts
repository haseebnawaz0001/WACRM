import { test, expect } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const LOCALES_DIR = path.resolve(__dirname, '../../../src/i18n/locales')

/**
 * Recursively collect all string values from a nested object,
 * returning their full dot-notation key paths.
 */
function collectStrings(obj: Record<string, unknown>, prefix = ''): Array<{ key: string; value: string }> {
  const entries: Array<{ key: string; value: string }> = []

  for (const [k, v] of Object.entries(obj)) {
    const fullKey = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'string') {
      entries.push({ key: fullKey, value: v })
    } else if (v && typeof v === 'object' && !Array.isArray(v)) {
      entries.push(...collectStrings(v as Record<string, unknown>, fullKey))
    }
  }

  return entries
}

/**
 * Check if a string contains unescaped double curly braces.
 *
 * vue-i18n uses {placeholder} for interpolation. Double curly braces {{ }}
 * cause a "Not allowed nest placeholder" compilation error in production.
 *
 * The escaped form uses vue-i18n's literal syntax: {'{{'}  and  {'}}'}
 * This function strips all literal blocks {' ... '} first, then checks
 * for remaining {{ or }} patterns.
 */
function findUnescapedDoubleCurlies(value: string): boolean {
  const stripped = value.replace(/\{'[^']*'\}/g, '')
  return stripped.includes('{{') || stripped.includes('}}')
}


/**
 * Find keys declared twice in the same object.
 *
 * JSON.parse keeps the last one and says nothing, so a second `"profile": {}`
 * block silently replaces the first. That is how the Profile page lost its
 * labels: a contact-profile namespace was added under a name already in use,
 * every `profile.*` string became the raw key, and nothing failed until the
 * page was opened. The raw text has to be scanned, because by the time the file
 * is parsed the evidence is gone.
 */
function findDuplicateKeys(raw: string): string[] {
  const duplicates: string[] = []
  const stack: Array<'object' | 'array'> = []
  const seen: Array<Set<string>> = []
  const path: string[] = []
  let awaitingKey = false
  let lastKey = ''
  let i = 0

  const readString = (): string => {
    let out = ''
    i++ // opening quote
    while (i < raw.length) {
      if (raw[i] === '\\') {
        out += raw[i] + raw[i + 1]
        i += 2
        continue
      }
      if (raw[i] === '"') {
        i++
        return out
      }
      out += raw[i]
      i++
    }
    return out
  }

  while (i < raw.length) {
    const c = raw[i]
    if (c === '"') {
      const text = readString()
      if (awaitingKey && stack[stack.length - 1] === 'object') {
        const here = seen[seen.length - 1]
        if (here.has(text)) {
          duplicates.push([...path, text].filter(Boolean).join('.'))
        }
        here.add(text)
        lastKey = text
        awaitingKey = false
      }
      continue
    }
    if (c === '{') {
      stack.push('object')
      seen.push(new Set())
      path.push(lastKey)
      lastKey = ''
      awaitingKey = true
    } else if (c === '}') {
      stack.pop()
      seen.pop()
      path.pop()
      awaitingKey = false
    } else if (c === '[') {
      stack.push('array')
    } else if (c === ']') {
      stack.pop()
    } else if (c === ',') {
      awaitingKey = stack[stack.length - 1] === 'object'
    }
    i++
  }

  return duplicates
}

test.describe('i18n Translation Validation', () => {
  test('locale files should not contain unescaped double curly braces', () => {
    const files = fs.readdirSync(LOCALES_DIR).filter(f => f.endsWith('.json'))
    expect(files.length).toBeGreaterThan(0)

    const violations: string[] = []

    for (const file of files) {
      const filePath = path.join(LOCALES_DIR, file)
      const content = JSON.parse(fs.readFileSync(filePath, 'utf-8'))
      const strings = collectStrings(content)

      for (const { key, value } of strings) {
        if (findUnescapedDoubleCurlies(value)) {
          violations.push(
            `${file} → "${key}": contains unescaped {{ or }}. ` +
            `Use {'{{'}  and {'}}'}  to escape. Value: "${value}"`
          )
        }
      }
    }

    expect(violations, `Found ${violations.length} unescaped double curly brace(s) in i18n files:\n${violations.join('\n')}`).toHaveLength(0)
  })

  test('locale files should be valid JSON', () => {
    const files = fs.readdirSync(LOCALES_DIR).filter(f => f.endsWith('.json'))

    for (const file of files) {
      const filePath = path.join(LOCALES_DIR, file)
      const raw = fs.readFileSync(filePath, 'utf-8')

      expect(() => JSON.parse(raw), `${file} is not valid JSON`).not.toThrow()
    }
  })

  test('locale files should not have empty translation values', () => {
    const files = fs.readdirSync(LOCALES_DIR).filter(f => f.endsWith('.json'))
    const empties: string[] = []

    for (const file of files) {
      const filePath = path.join(LOCALES_DIR, file)
      const content = JSON.parse(fs.readFileSync(filePath, 'utf-8'))
      const strings = collectStrings(content)

      for (const { key, value } of strings) {
        if (value.trim() === '') {
          empties.push(`${file} → "${key}" is empty`)
        }
      }
    }

    expect(empties, `Found ${empties.length} empty translation(s):\n${empties.join('\n')}`).toHaveLength(0)
  })
  test('locale files should not declare a key twice', () => {
    const files = fs.readdirSync(LOCALES_DIR).filter(f => f.endsWith('.json'))
    const clashes: string[] = []

    for (const file of files) {
      const raw = fs.readFileSync(path.join(LOCALES_DIR, file), 'utf-8')
      for (const key of findDuplicateKeys(raw)) {
        clashes.push(`${file} → "${key}" is declared more than once; the later block silently replaces the earlier one`)
      }
    }

    expect(clashes, `Found ${clashes.length} duplicate key(s):\n${clashes.join('\n')}`).toHaveLength(0)
  })
})
