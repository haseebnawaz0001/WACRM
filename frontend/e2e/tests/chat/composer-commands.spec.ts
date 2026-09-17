import { test, expect, request as playwrightRequest } from '@playwright/test'
import { loginAsAdmin } from '../../helpers'
import { ApiHelper } from '../../helpers/api'
import { ChatPage } from '../../pages'
import { createTestScope } from '../../framework'

const scope = createTestScope('composer-commands')

/**
 * Composer commands (plan 10, 4.1).
 *
 * Promising a follow-up, noting what was said and taking the conversation all
 * lived in different corners of the screen, so doing any of them meant leaving
 * the sentence half-typed.
 */
test.describe('Composer commands', () => {
  test.describe.configure({ mode: 'serial' })
  test.setTimeout(60000)

  let chatPage: ChatPage
  let contactId: string
  let api: ApiHelper

  test.beforeAll(async () => {
    const context = await playwrightRequest.newContext()
    api = new ApiHelper(context)
    await api.loginAsAdmin()
    const created = await api.createContact(scope.phone(), scope.name('cmd'))
    contactId = created.id ?? created.contact?.id
  })

  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
    chatPage = new ChatPage(page)
    await chatPage.goto(contactId)
  })

  test('typing a slash offers the commands it could be', async ({ page }) => {
    const input = page.locator('textarea').first()
    await input.fill('/t')

    const hints = page.locator('[data-command-hints]')
    await expect(hints).toBeVisible()
    await expect(hints).toContainText('/task')
  })

  test('a word that is no command leaves the composer alone', async ({ page }) => {
    const input = page.locator('textarea').first()
    await input.fill('/zzz')
    await expect(page.locator('[data-command-hints]')).toHaveCount(0)
  })

  test('/task creates the follow-up and clears the line', async ({ page }) => {
    const title = scope.name('from-composer')
    const input = page.locator('textarea').first()

    await input.fill(`/task ${title}`)
    await expect(page.locator('[data-command-hints]')).toBeVisible()
    await input.press('Enter')

    // The line is not sent to the customer.
    await expect(input).toHaveValue('')

    const tasks = await api.get(`/api/tasks?view=all&contact_id=${contactId}`)
    expect(tasks.status()).toBe(200)
    const titles = ((await tasks.json()).data.tasks as any[]).map(t => t.title)
    expect(titles).toContain(title)
  })

  test('/note records it internally rather than messaging the customer', async ({ page }) => {
    const body = scope.name('noted')
    const input = page.locator('textarea').first()

    await input.fill(`/note ${body}`)
    await input.press('Enter')
    await expect(input).toHaveValue('')

    const notes = await api.get(`/api/contacts/${contactId}/notes`)
    expect(notes.status()).toBe(200)
    const contents = ((await notes.json()).data.notes as any[]).map(n => n.content)
    expect(contents).toContain(body)
  })
})
