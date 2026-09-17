import { test, expect, APIRequestContext } from '@playwright/test'
import { ApiHelper } from '../../helpers'

/**
 * The end-to-end journeys from docs/feature-plans/10-system-integration.md §5.
 *
 * Each one crosses several plans on purpose: the point is not that a module
 * works, it is that the modules are one system. A contact created by one plan
 * has to be the contact another plan's rule fires on, and the events that join
 * them have to actually arrive.
 *
 * These run against the API rather than the UI. That is the exception the e2e
 * architecture allows for behaviour with no UI surface, and it applies here:
 * the thing under test is the chain of records and events between the screens,
 * and driving it through nine pages would test the screens instead of the
 * chain.
 */

/** A unique phone number, so parallel runs never collide on one contact. */
function uniquePhone(): string {
  return '1555' + String(Date.now()).slice(-6) + String(Math.floor(Math.random() * 90) + 10)
}

async function adminApi(request: APIRequestContext): Promise<ApiHelper> {
  const api = new ApiHelper(request)
  await api.loginAsAdmin()
  return api
}

async function createContact(api: ApiHelper, name: string): Promise<string> {
  const resp = await api.post('/api/contacts', {
    phone_number: uniquePhone(),
    profile_name: name,
    whatsapp_account: 'acct'
  })
  expect(resp.status(), await resp.text()).toBeLessThan(300)
  const body = await resp.json()
  return body.data.id ?? body.data.contact?.id
}

test.describe('J-CRM: a contact becomes a record', () => {
  /**
   * Plan 01 + plan 02 + plan 10 S6. A contact edited through the bulk action
   * carries typed fields, those fields are addressable in the one template
   * namespace, and the edit lands on the timeline.
   *
   * Before the namespace existed, `contact.fields.company` resolved in exactly
   * one screen; before the timeline, a field edit left no trace at all.
   */
  test('a custom field set in bulk is addressable in templates and lands on the timeline', async ({ request }) => {
    const api = await adminApi(request)
    const contactID = await createContact(api, 'Journey Fields')

    const bulk = await api.post('/api/contacts/bulk', {
      contact_ids: [contactID],
      add_tags: ['journey'],
      fields: { company: 'Kano Logistics' }
    })
    expect(bulk.status()).toBe(200)
    expect((await bulk.json()).data.updated).toBe(1)

    // Addressable in a template, through the shared namespace.
    const canned = await api.post('/api/canned-responses', {
      name: `journey-${Date.now()}`,
      content: 'Hi {{contact_name}} at {{contact.fields.company}}'
    })
    expect(canned.status()).toBeLessThan(300)
    const cannedID = (await canned.json()).data.id

    const rendered = await api.post(`/api/canned-responses/${cannedID}/resolve`, {
      contact_id: contactID
    })
    expect(rendered.status()).toBe(200)
    const content = (await rendered.json()).data.content
    expect(content).toContain('Journey Fields')
    expect(content).toContain('Kano Logistics')
    expect(content).not.toContain('{{')

    // And it is on the contact's history. The relay is asynchronous, so this
    // polls rather than asserting once — an event that arrives a beat later is
    // not a defect.
    await expect
      .poll(async () => {
        const timeline = await api.get(`/api/contacts/${contactID}/timeline`)
        const items = (await timeline.json()).data?.items ?? []
        return items.some((i: any) => i.type === 'field_change' || i.type === 'tag')
      }, { timeout: 20000, message: 'the field edit should reach the timeline' })
      .toBe(true)
  })
})

test.describe('J2: a follow-up is promised, rescheduled and kept', () => {
  /**
   * Plan 04. A task can be created, moved and reopened without losing its
   * history — before this it could only be cancelled and recreated, which lost
   * the original and its place on the contact's timeline.
   */
  test('a task survives a reschedule, a completion and a reopen', async ({ request }) => {
    const api = await adminApi(request)
    const contactID = await createContact(api, 'Journey Task')

    const types = await api.get('/api/task-types')
    const typeID = (await types.json()).data.task_types[0].id

    const created = await api.post('/api/tasks', {
      contact_id: contactID,
      type_id: typeID,
      title: 'Call back'
    })
    expect(created.status()).toBeLessThan(300)
    const taskID = (await created.json()).data.task.id

    const rescheduled = await api.put(`/api/tasks/${taskID}`, {
      title: 'Call back on Monday',
      due_at: '2027-01-04T10:00:00Z'
    })
    expect(rescheduled.status()).toBe(200)
    expect((await rescheduled.json()).data.task.title).toBe('Call back on Monday')

    const completed = await api.post(`/api/tasks/${taskID}/complete`, {})
    expect((await completed.json()).data.task.status).toBe('completed')

    // A finished promise is not re-negotiable; reopen it first.
    const blocked = await api.put(`/api/tasks/${taskID}`, { title: 'sneaky edit' })
    expect(blocked.status()).toBe(409)

    const reopened = await api.post(`/api/tasks/${taskID}/reopen`, {})
    expect((await reopened.json()).data.task.status).toBe('open')

    // The badge counts what can be acted on now, and a reopened task can.
    const list = await api.get('/api/tasks?view=mine')
    const body = (await list.json()).data
    expect(body).toHaveProperty('due_count')
    expect(body.tasks.some((t: any) => t.id === taskID)).toBe(true)
  })
})

test.describe('J4: a conversation moves through its states', () => {
  /**
   * Plan 03 + plan 10 S5. Open → pending → resolved → reopened, with the
   * inbox views agreeing at each step.
   *
   * "Mark pending" and "Reopen" did not exist: an agent who resolved a
   * conversation by mistake had to wait for the customer to write again, and
   * "open" meant both "needs me" and "waiting on them".
   */
  test('a conversation can be marked pending, resolved and reopened', async ({ request }) => {
    const api = await adminApi(request)
    const contactID = await createContact(api, 'Journey Conversation')

    // A conversation is opened by a customer writing in, so the journey starts
    // there rather than reaching for a shortcut: the point of this test is the
    // real path, and a conversation conjured another way would not exercise it.
    const opened = await openConversationViaWebhook(api, contactID)
    expect(opened, 'an inbound message should open a conversation').toBe(true)

    const pending = await api.post('/api/conversations/pending', { contact_id: contactID })
    expect(pending.status()).toBe(200)
    expect((await pending.json()).data.conversation.status).toBe('pending')

    const resolved = await api.post('/api/conversations/resolve', { contact_id: contactID })
    expect(resolved.status()).toBe(200)
    const conversation = (await resolved.json()).data.conversation
    expect(conversation.status).toBe('resolved')
    // Nobody is handling something that is finished.
    expect(conversation.handling).toBe('none')

    const reopened = await api.post('/api/conversations/reopen', {
      conversation_id: conversation.id
    })
    expect(reopened.status()).toBe(200)
    const after = (await reopened.json()).data.conversation
    expect(after.status).toBe('open')
    expect(after.reopened_count).toBeGreaterThan(0)
  })
})

test.describe('J5: import, duplicate, merge', () => {
  /**
   * Plan 06 + plan 10 S8. Merging joins two records into one, and everything
   * that referenced the loser follows — which is the part that used to be a
   * literal list of seven tables with everything else left behind.
   */
  test('merging a duplicate moves its work to the survivor', async ({ request }) => {
    const api = await adminApi(request)
    const primaryID = await createContact(api, 'Journey Primary')
    const secondaryID = await createContact(api, 'Journey Secondary')

    const types = await api.get('/api/task-types')
    const typeID = (await types.json()).data.task_types[0].id

    const task = await api.post('/api/tasks', {
      contact_id: secondaryID,
      type_id: typeID,
      title: 'Owed to the duplicate'
    })
    expect(task.status()).toBeLessThan(300)
    const taskID = (await task.json()).data.task.id

    const merged = await api.post('/api/contacts/merge', {
      primary_id: primaryID,
      secondary_id: secondaryID
    })
    expect(merged.status(), await merged.text()).toBe(200)

    // The follow-up somebody promised is now owed against the surviving
    // record, not filed under an id nobody can open.
    const tasks = await api.get(`/api/tasks?contact_id=${primaryID}&status=all`)
    const moved = (await tasks.json()).data.tasks
    expect(moved.some((t: any) => t.id === taskID)).toBe(true)

    // And the merge is on the record, so "what happened here" is answerable.
    const merges = await api.get(`/api/contacts/${primaryID}/merges`)
    expect(merges.status()).toBe(200)
    expect((await merges.json()).data.merges.length).toBeGreaterThan(0)
  })
})

test.describe('J6: an admin changes the organization', () => {
  /**
   * Plan 03 + plan 10 §4.8. The inbox rules are an organization setting rather
   * than something buried in the chatbot's SLA config — which is what made
   * "close stale conversations" require response-time alerting to be on.
   */
  test('inbox rules round-trip and refuse nonsense', async ({ request }) => {
    const api = await adminApi(request)

    const saved = await api.put('/api/org/settings', {
      inbox: {
        reopen_window_hours: 12,
        auto_resolve_idle_hours: 72,
        pending_timeout_hours: 48,
        auto_pending_on_agent_reply: true
      }
    })
    expect(saved.status()).toBe(200)

    const read = await api.get('/api/org/settings')
    const inbox = (await read.json()).data.settings.inbox
    expect(inbox.reopen_window_hours).toBe(12)
    expect(inbox.auto_resolve_idle_hours).toBe(72)
    expect(inbox.auto_pending_on_agent_reply).toBe(true)

    const bad = await api.put('/api/org/settings', {
      inbox: {
        reopen_window_hours: -1,
        auto_resolve_idle_hours: 0,
        pending_timeout_hours: 0,
        auto_pending_on_agent_reply: false
      }
    })
    expect(bad.status()).toBe(400)

    // Put it back, so a shared database is not left with a surprising setting.
    await api.put('/api/org/settings', {
      inbox: {
        reopen_window_hours: 24,
        auto_resolve_idle_hours: 0,
        pending_timeout_hours: 0,
        auto_pending_on_agent_reply: false
      }
    })
  })
})

test.describe('Permission enforcement', () => {
  /**
   * Plan 10 S1. The route table and its startup self-check are the guard, but
   * the guard is a declaration — this checks the handlers actually refuse.
   *
   * These endpoints are exactly the ones that shipped unprotected: an agent
   * without the permission used to get 200.
   */
  test('an agent is refused the endpoints they have no permission for', async ({ request }) => {
    const api = new ApiHelper(request)
    await api.login('agent@test.com', 'password')

    // A rule one agent writes acts on everybody's customers, and a webhook
    // carries the organization's data off-site. Neither is an agent's to set up.
    for (const path of ['/api/automations', '/api/webhooks']) {
      const resp = await api.get(path)
      expect(resp.status(), `${path} should refuse an agent`).toBe(403)
    }
  })

  test('an agent may still reach the pages that are theirs', async ({ request }) => {
    const api = new ApiHelper(request)
    await api.login('agent@test.com', 'password')

    for (const path of [
      '/api/tasks?view=mine',
      '/api/inbox/counts',
      '/api/variables?context=canned',
      // Agents hold segments:read by design: filtering a list for yourself is
      // not the same decision as saving an audience a campaign will message.
      '/api/segments'
    ]) {
      const resp = await api.get(path)
      expect(resp.status(), `${path} should be allowed`).toBeLessThan(400)
    }
  })
})

/**
 * Deliver a synthetic inbound message, the way Meta would.
 *
 * Returns whether a conversation is now open. It polls because the webhook is
 * accepted and processed asynchronously — the handler answers Meta immediately,
 * which is the behaviour Meta requires.
 */
async function openConversationViaWebhook(api: ApiHelper, contactID: string): Promise<boolean> {
  const contact = await api.get(`/api/contacts/${contactID}`)
  const body = await contact.json()
  const phone: string = (body.data.contact ?? body.data).phone_number

  const account = await ensureAccount(api)
  if (!account) return false

  const resp = await api.post('/api/webhook', {
    object: 'whatsapp_business_account',
    entry: [
      {
        id: account.business_id ?? 'biz',
        changes: [
          {
            field: 'messages',
            value: {
              messaging_product: 'whatsapp',
              metadata: { display_phone_number: phone, phone_number_id: account.phone_id },
              contacts: [{ profile: { name: 'Journey Conversation' }, wa_id: phone }],
              messages: [
                {
                  from: phone,
                  id: `wamid.journey.${Date.now()}`,
                  timestamp: String(Math.floor(Date.now() / 1000)),
                  type: 'text',
                  text: { body: 'Hello, I need help' }
                }
              ]
            }
          }
        ]
      }
    ]
  })
  if (resp.status() >= 300) return false

  let opened = false
  for (let attempt = 0; attempt < 20 && !opened; attempt++) {
    const conv = await api.get(`/api/contacts/${contactID}/conversation`)
    if (conv.status() === 200) {
      const payload = (await conv.json()).data
      opened = !!(payload?.conversation ?? payload)?.id
    }
    if (!opened) await new Promise(resolve => setTimeout(resolve, 500))
  }
  return opened
}

/**
 * A WhatsApp account for the inbound path to resolve.
 *
 * The webhook finds the account by phone_number_id; without one the message is
 * dropped and no conversation opens. Creating it does not touch Meta — the
 * credentials are only used when the product sends — so a test can make one.
 */
async function ensureAccount(api: ApiHelper): Promise<any | null> {
  const existing = await api.get('/api/accounts')
  const accounts = (await existing.json()).data?.accounts ?? []
  if (accounts.length) return accounts[0]

  const created = await api.post('/api/accounts', {
    name: `e2e-journeys-${Date.now()}`,
    phone_id: `e2e-phone-${Date.now()}`,
    business_id: `e2e-biz-${Date.now()}`,
    access_token: 'e2e-token'
  })
  if (created.status() >= 300) return null
  return (await created.json()).data
}
