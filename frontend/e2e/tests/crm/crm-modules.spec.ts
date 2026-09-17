import { test, expect } from '@playwright/test'
import { loginAsAdmin, login, ApiHelper } from '../../helpers'
import {
  HomePage,
  InboxPage,
  ContactsModulePage,
  TasksPage,
  SegmentsPage,
  PipelinePage,
  AutomationsPage,
  CrmReportsPage,
  DuplicatesPage,
  expectPageLoaded
} from '../../pages/CrmPages'

/**
 * The CRM modules render and are reachable.
 *
 * Nine modules shipped across plans 01–09 with no e2e coverage at all, so a
 * route that 404s, a view that throws on mount or a permission gate that
 * bounced the wrong people would have reached production invisibly. These are
 * deliberately shallow — "does it open, for the people it is meant for" — which
 * is the check that catches the whole class of breakage the deeper tests below
 * assume away.
 */
test.describe('CRM modules', () => {
  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
  })

  test('Home shows what is waiting for the signed-in user', async ({ page }) => {
    const home = new HomePage(page)
    await home.goto()

    await expect(page).not.toHaveURL(/\/login/)
    await expect(home.conversations.or(home.tasks).first()).toBeVisible({ timeout: 15000 })
  })

  test('Inbox opens on every view', async ({ page }) => {
    const inbox = new InboxPage(page)

    for (const view of ['mine', 'unassigned', 'bot', 'all']) {
      await inbox.goto(view)
      await expect(page).not.toHaveURL(/\/login/)
      await expect(page.locator('body')).not.toContainText('NotFound')
    }
  })

  test('Contacts, Tasks, Segments, Pipeline, Automations and CRM Reports all open', async ({ page }) => {
    for (const module of [
      new ContactsModulePage(page),
      new TasksPage(page),
      new SegmentsPage(page),
      new PipelinePage(page),
      new AutomationsPage(page),
      new CrmReportsPage(page),
      new DuplicatesPage(page)
    ]) {
      await module.goto()
      await expect(page).not.toHaveURL(/\/login/)
      // A view that threw on mount leaves an empty shell; the nav surviving is
      // the cheapest proof the page itself rendered.
      await expect(page.locator('nav').first()).toBeVisible({ timeout: 15000 })
    }
  })

  /**
   * Contacts moved out of Settings into their own module (plan 01). The old
   * paths are in people's bookmarks and in links colleagues sent each other, so
   * they redirect rather than 404.
   */
  test('the old Settings → Contacts path redirects to the module', async ({ page }) => {
    await page.goto('/settings/contacts')
    await page.waitForLoadState('domcontentloaded')
    await expect(page).toHaveURL(/\/contacts$/)
  })

  /**
   * An agent cannot open the Dashboard — it needs `analytics` — so signing in
   * used to put them on a page they could not see. Home has no permission.
   *
   * On its own user rather than the shared agent@test.com fixture: other specs
   * reassign that account's role while they run, so borrowing it makes this
   * test pass or fail on what else happens to be running.
   */
  test('a user with no analytics permission can open Home', async ({ page, request }) => {
    const api = new ApiHelper(request)
    await api.loginAsAdmin()

    const suffix = Date.now()
    const role = await api.createRole({
      name: `E2E-home-${suffix}`,
      description: 'Chat only, no analytics',
      permissions: ['chat:read', 'chat:write']
    })
    const user = await api.createUser({
      email: `e2e-home-${suffix}@test.com`,
      password: 'password',
      full_name: 'Home Only',
      role_id: role.id
    })

    try {
      await login(page, { email: user.email, password: 'password', role: 'agent' })
      const home = new HomePage(page)
      await home.goto()

      await expect(page).not.toHaveURL(/\/login/)
      await expect(page).toHaveURL(/\/home/)
    } finally {
      await api.deleteUser(user.id).catch(() => {})
      await api.deleteRole(role.id).catch(() => {})
    }
  })
})

/**
 * The backend catalogs the UI is built from.
 *
 * These have no UI surface of their own — they are what the variable pickers
 * and the rule builder render from — so per the e2e architecture they are
 * tested through the API.
 */
test.describe('CRM catalogs', () => {
  test('the variable catalog expands the organization\'s own custom fields', async ({ request }) => {
    const api = new ApiHelper(request)
    await api.loginAsAdmin()

    const resp = await api.get('/api/variables?context=canned')
    expect(resp.status()).toBe(200)

    const body = await resp.json()
    const paths: string[] = body.data.variables.map((v: any) => v.path)

    expect(paths).toContain('contact.name')
    expect(paths).toContain('contact.owner.name')
    // The generic "contact.fields." entry is replaced by the real fields, so a
    // picker offers "Company" rather than asking an author to remember a key.
    expect(paths.some(p => p.startsWith('contact.fields.') && p !== 'contact.fields.')).toBe(true)
    expect(paths).not.toContain('contact.fields.')
  })

  test('a campaign context does not offer variables it cannot resolve', async ({ request }) => {
    const api = new ApiHelper(request)
    await api.loginAsAdmin()

    const resp = await api.get('/api/variables?context=campaign')
    expect(resp.status()).toBe(200)

    const groups: string[] = (await resp.json()).data.variables.map((v: any) => v.group)
    // A campaign has no acting user and no one conversation; offering
    // `user.name` would be a promise the render cannot keep.
    expect(groups).not.toContain('user')
    expect(groups).not.toContain('conversation')
  })

  test('an unknown variable context is refused rather than guessed', async ({ request }) => {
    const api = new ApiHelper(request)
    await api.loginAsAdmin()

    const resp = await api.get('/api/variables?context=nonsense')
    expect(resp.status()).toBe(400)
  })

  test('the automation catalog offers the CRM triggers the plans added', async ({ request }) => {
    const api = new ApiHelper(request)
    await api.loginAsAdmin()

    const resp = await api.get('/api/automations/catalog')
    expect(resp.status()).toBe(200)

    const types: string[] = (await resp.json()).data.triggers.map((t: any) => t.type)
    for (const expected of [
      'contact.lifecycle_stage_changed',
      'conversation.sla_breached',
      'call.missed',
      'chatbot.flow_completed',
      'campaign.replied'
    ]) {
      expect(types).toContain(expected)
    }
  })
})
