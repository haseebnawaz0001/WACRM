import { test, expect, request as playwrightRequest } from '@playwright/test'
import { loginAsAdmin } from '../../helpers'
import { ApiHelper } from '../../helpers/api'
import { ChatPage } from '../../pages'
import { createTestScope } from '../../framework'

const scope = createTestScope('message-actions')

const MESSAGE = 'My order number is ORD-55123'

/**
 * Message actions (plan 10, 4.1).
 *
 * The thing worth acting on is usually a sentence the customer just wrote — an
 * address, an order number, a promise to call back. Retyping it into a task, a
 * note or a field is how it gets typed wrong, so the actions start from the
 * message itself.
 */
test.describe('Message actions', () => {
  test.describe.configure({ mode: 'serial' })
  test.setTimeout(90000)

  let chatPage: ChatPage
  let contactId: string
  let api: ApiHelper
  let seeded = false

  test.beforeAll(async () => {
    const context = await playwrightRequest.newContext()
    api = new ApiHelper(context)
    await api.loginAsAdmin()

    const created = await api.createContact(scope.phone(), scope.name('actions'))
    contactId = created.id ?? created.contact?.id
    seeded = await sendInbound(api, contactId, MESSAGE)
  })

  test.beforeEach(async ({ page }) => {
    test.skip(!seeded, 'the inbound message could not be delivered in this environment')
    await loginAsAdmin(page)
    chatPage = new ChatPage(page)
    await chatPage.goto(contactId)
    await expect(page.getByText(MESSAGE).first()).toBeVisible({ timeout: 15000 })
  })

  /** Hover reveals the action column, then open its menu. */
  async function openMessageMenu(page: import('@playwright/test').Page) {
    const bubble = page.getByText(MESSAGE).first()
    await bubble.hover()
    const trigger = page.locator('[data-message-actions]').first()
    await trigger.click()
  }

  test('a message offers the actions that start from it', async ({ page }) => {
    await openMessageMenu(page)
    await expect(page.getByRole('button', { name: 'Create a follow-up' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Add a note about it' })).toBeVisible()
  })

  test('a follow-up is raised with the message as its title', async ({ page }) => {
    await openMessageMenu(page)
    // The menu fires the write and closes without waiting for it, which is the
    // right thing for somebody clicking. It does mean the assertion below has
    // to wait for the write itself: `api` is a separate request context, so it
    // is perfectly capable of reading the tasks list before the browser's POST
    // has been committed, and did.
    await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/tasks') && r.request().method() === 'POST'),
      page.getByRole('button', { name: 'Create a follow-up' }).click()
    ])

    const tasks = await api.get(`/api/tasks?view=all&contact_id=${contactId}`)
    expect(tasks.status()).toBe(200)
    const titles = ((await tasks.json()).data.tasks as any[]).map(t => t.title)
    expect(titles).toContain(MESSAGE)
  })

  test('a note quotes the message, so it still reads once the thread moves on', async ({ page }) => {
    await openMessageMenu(page)
    await Promise.all([
      page.waitForResponse(r => /\/api\/contacts\/[^/]+\/notes/.test(r.url()) && r.request().method() === 'POST'),
      page.getByRole('button', { name: 'Add a note about it' }).click()
    ])

    const notes = await api.get(`/api/contacts/${contactId}/notes`)
    expect(notes.status()).toBe(200)
    const contents = ((await notes.json()).data.notes as any[]).map(n => n.content)
    expect(contents.some(c => c.includes(MESSAGE))).toBe(true)
  })
})

/**
 * An inbound message, through the webhook the product already listens on —
 * there is no API that fabricates one, because messages come from Meta.
 */
async function sendInbound(api: ApiHelper, contactID: string, body: string): Promise<boolean> {
  const contact = await api.get(`/api/contacts/${contactID}`)
  const payload = await contact.json()
  const contactRow = payload.data.contact ?? payload.data
  const phone: string = contactRow.phone_number
  // The webhook overwrites profile_name from this payload, so it has to repeat
  // the scoped name. A literal here renamed the contact out of the E2E prefix
  // and the teardown then walked straight past it, which is how six "Message
  // Actions" rows ended up living in the demo database.
  const profileName: string = contactRow.profile_name

  const account = await ensureAccount(api)
  if (!account) return false

  const resp = await api.post('/api/webhook', {
    object: 'whatsapp_business_account',
    entry: [{
      id: account.business_id ?? 'biz',
      changes: [{
        field: 'messages',
        value: {
          messaging_product: 'whatsapp',
          metadata: { display_phone_number: phone, phone_number_id: account.phone_id },
          contacts: [{ profile: { name: profileName }, wa_id: phone }],
          messages: [{
            from: phone,
            id: `wamid.actions.${Date.now()}`,
            timestamp: String(Math.floor(Date.now() / 1000)),
            type: 'text',
            text: { body }
          }]
        }
      }]
    }]
  })
  return resp.status() < 300
}

/**
 * An account for the webhook to resolve. It finds the account by
 * phone_number_id; without one the message is dropped and nothing arrives.
 * Creating it does not touch Meta — the credentials are only used when the
 * product sends.
 */
async function ensureAccount(api: ApiHelper): Promise<any | null> {
  const existing = await api.get('/api/accounts')
  const accounts = (await existing.json()).data?.accounts ?? []
  if (accounts.length) return accounts[0]

  const created = await api.post('/api/accounts', {
    name: `e2e-message-actions-${Date.now()}`,
    phone_id: `e2e-phone-${Date.now()}`,
    business_id: `e2e-biz-${Date.now()}`,
    access_token: 'e2e-token'
  })
  if (created.status() >= 300) return null
  return (await created.json()).data
}
