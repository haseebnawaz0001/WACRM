import { Page, Locator, expect } from '@playwright/test'
import { BasePage } from './BasePage'

/**
 * Page objects for the CRM modules added by docs/feature-plans (01–09).
 *
 * One file rather than nine: each of these pages is a list with a couple of
 * controls, and nine near-identical classes in nine files would be harder to
 * read than the pages themselves. They split out when one grows real behaviour.
 */

export class HomePage extends BasePage {
  readonly conversations: Locator
  readonly tasks: Locator

  constructor(page: Page) {
    super(page)
    this.conversations = page.getByText('My conversations')
    this.tasks = page.getByText('My follow-ups')
  }

  async goto() {
    await this.page.goto('/home')
    await this.page.waitForLoadState('domcontentloaded')
  }
}

export class InboxPage extends BasePage {
  constructor(page: Page) {
    super(page)
  }

  async goto(view?: string) {
    await this.page.goto(view ? `/inbox?view=${view}` : '/inbox')
    await this.page.waitForLoadState('domcontentloaded')
  }

  /** The tab for one view. Views are Mine / Unassigned / Bot / All. */
  tab(name: RegExp): Locator {
    return this.page.getByRole('button', { name }).first()
  }

  row(text: string): Locator {
    return this.page.locator('li').filter({ hasText: text }).first()
  }
}

export class ContactsModulePage extends BasePage {
  async goto() {
    await this.page.goto('/contacts')
    await this.page.waitForLoadState('domcontentloaded')
  }

  async openProfile(contactId: string) {
    await this.page.goto(`/contacts/${contactId}`)
    await this.page.waitForLoadState('domcontentloaded')
  }
}

export class TasksPage extends BasePage {
  async goto() {
    await this.page.goto('/tasks')
    await this.page.waitForLoadState('domcontentloaded')
  }

  row(title: string): Locator {
    return this.page.locator('li, tr').filter({ hasText: title }).first()
  }
}

export class SegmentsPage extends BasePage {
  async goto() {
    await this.page.goto('/segments')
    await this.page.waitForLoadState('domcontentloaded')
  }
}

export class PipelinePage extends BasePage {
  async goto() {
    await this.page.goto('/pipeline')
    await this.page.waitForLoadState('domcontentloaded')
  }
}

export class AutomationsPage extends BasePage {
  async goto() {
    await this.page.goto('/automations')
    await this.page.waitForLoadState('domcontentloaded')
  }
}

export class CrmReportsPage extends BasePage {
  async goto() {
    await this.page.goto('/analytics/crm')
    await this.page.waitForLoadState('domcontentloaded')
  }
}

export class DuplicatesPage extends BasePage {
  async goto() {
    await this.page.goto('/contacts/duplicates')
    await this.page.waitForLoadState('domcontentloaded')
  }
}

/**
 * Assert a page rendered rather than falling over.
 *
 * A route that 404s, throws during setup or bounces to login all produce a
 * page with no error text and nothing to see — which is indistinguishable from
 * an empty list unless the check is explicit.
 */
export async function expectPageLoaded(page: Page, heading: RegExp) {
  await expect(page).not.toHaveURL(/\/login/)
  await expect(page.getByRole('heading', { name: heading }).first()).toBeVisible({ timeout: 15000 })
}
