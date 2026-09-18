/**
 * Per-spec scoping primitive.
 *
 * Each spec creates a TestScope at module load — every name, email and
 * phone derived from it includes a stable prefix unique to this run.
 *
 * Why: with no automatic per-test cleanup, test artifacts persist in the
 * dev database. Prefixed names mean (a) tests within a single run can't
 * collide on uniqueness, (b) leftover rows are identifiable as test data
 * if a dev wants to clean their local DB, (c) tests that filter / list
 * by name don't accidentally match production-shaped data.
 *
 *   const scope = createTestScope('users')
 *   scope.prefix    // 'E2E-users-mh3p2x'
 *   scope.name()    // 'E2E-users-mh3p2x-a3f9b1'
 *   scope.name('admin')  // 'E2E-users-mh3p2x-admin'
 *   scope.email('agent') // 'e2e-users-mh3p2x-agent@e2e.test'
 *   scope.phone()        // '911745236847123'
 */

import { randomBytes, randomInt } from 'node:crypto'

export interface TestScope {
  /** Stable identifier for everything this spec creates. */
  readonly prefix: string
  /** Generates a unique name; `suffix` is appended if provided, otherwise random. */
  name(suffix?: string): string
  /** Generates a unique email under @e2e.test. */
  email(suffix?: string): string
  /** Generates a unique phone-shaped string (no SMS sent in test runs). */
  phone(): string
}

export function createTestScope(specName: string): TestScope {
  const safe = specName.replace(/[^a-zA-Z0-9-]/g, '-').toLowerCase()
  const runId = Date.now().toString(36)
  const prefix = `E2E-${safe}-${runId}`

  return {
    prefix,
    name(suffix) {
      return suffix ? `${prefix}-${suffix}` : `${prefix}-${randomSuffix()}`
    },
    email(suffix) {
      const local = suffix ?? randomSuffix()
      return `${prefix.toLowerCase()}-${local}@e2e.test`
    },
    phone() {
      return e2ePhone()
    },
  }
}

function randomSuffix(): string {
  return randomBytes(4).toString('hex').slice(0, 6)
}

/**
 * The phone prefix every E2E contact carries.
 *
 * A contact is not always recognisable by its name: the webhook rewrites
 * profile_name from whatever Meta sends, and a contact created from a phone
 * number alone has no name at all. Both slipped past a teardown that matched on
 * names, so the demo database grew by tens of rows a day. The phone number is
 * the one field every one of them has and nothing renames, so it carries the
 * marker instead.
 *
 * 9199 keeps the country code real-looking while reserving a range no demo or
 * seed data uses.
 */
export const E2E_PHONE_PREFIX = '9199'

/** A phone-shaped string inside the reserved E2E range. No SMS is ever sent. */
export function e2ePhone(): string {
  // prefix + 5 digits of the clock + 3 random. node:crypto satisfies CodeQL's
  // js/insecure-randomness — these values flow into API calls, so the linter
  // treats them as security-sensitive even though the phones are throwaway.
  const suffix = randomInt(0, 1000).toString().padStart(3, '0')
  return `${E2E_PHONE_PREFIX}${Date.now().toString().slice(-5)}${suffix}`
}
