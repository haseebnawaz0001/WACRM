import { test, expect, request as playwrightRequest } from '@playwright/test'
import { loginAsAdmin } from '../../helpers'
import { ApiHelper } from '../../helpers/api'
import { ChatPage } from '../../pages'
import { createTestScope } from '../../framework'

const scope = createTestScope('thread-activity')

/**
 * Thread activity pills (plan 10, 4.1).
 *
 * The chat and the contact's timeline used to tell different stories: an agent
 * reading a thread could not see that the conversation had been reassigned, a
 * task raised off it, or a field set by an automation — all of which the
 * customer's next message might be replying to.
 */
test.describe('Thread activity', () => {
  test.describe.configure({ mode: 'serial' })
  test.setTimeout(60000)

  let chatPage: ChatPage
  let contactId: string

  test.beforeAll(async () => {
    const context = await playwrightRequest.newContext()
    const api = new ApiHelper(context)
    await api.loginAsAdmin()

    const created = await api.createContact(scope.phone(), scope.name('pills'))
    contactId = created.id ?? created.contact?.id

    // Something for a pill to describe. A tag change is the cheapest activity
    // that every organization records.
    await api.put(`/api/contacts/${contactId}/tags`, { tags: ['pill-check'] })
    await context.dispose()
  })

  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
    chatPage = new ChatPage(page)
    await chatPage.goto(contactId)
  })

  test('the toggle is offered on a conversation', async ({ page }) => {
    await expect(page.locator('#activity-toggle')).toBeVisible()
  })

  test('activity is hidden until it is asked for, and then shown', async ({ page }) => {
    const toggle = page.locator('#activity-toggle')
    const pills = page.locator('[data-activity-pill]')

    // Off by default: a thread is a conversation first.
    await expect(pills).toHaveCount(0)

    await toggle.click()
    await expect(pills.first()).toBeVisible({ timeout: 10000 })
  })

  test('the choice survives a reload, because it is a preference not a mode', async ({ page }) => {
    // Self-contained: each test gets its own browser context, so nothing
    // carries over from the one above.
    const pills = page.locator('[data-activity-pill]')

    await page.locator('#activity-toggle').click()
    await expect(pills.first()).toBeVisible({ timeout: 10000 })

    await page.reload()
    await expect(pills.first()).toBeVisible({ timeout: 10000 })

    await page.locator('#activity-toggle').click()
    await expect(pills).toHaveCount(0)

    await page.reload()
    await expect(pills).toHaveCount(0)
  })
})
