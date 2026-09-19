import { test, expect } from '@playwright/test'
import { loginAsAdmin } from '../../helpers/auth'
import { ApiHelper } from '../../helpers'

// The widget builder asks what to see, how to show it and what to include —
// no data sources, metrics or typed-in ids. These walk the path a person takes.

const NAME = 'E2E-builder follow-ups by type'

test.describe('Widget builder', () => {
  test.describe.configure({ mode: 'serial' })

  test.afterAll(async ({ request }) => {
    const api = new ApiHelper(request)
    await api.loginAsAdmin()
    const res = await api.get('/api/widgets')
    for (const w of (await res.json())?.data?.widgets || []) {
      if (String(w.name).startsWith('E2E-builder')) await api.del(`/api/widgets/${w.id}`)
    }
  })

  test('builds a widget from a measure, with a split and a condition', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto('/')
    await page.getByRole('button', { name: 'Add Widget' }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('What do you want to see?')).toBeVisible()

    // Found by searching in plain words.
    await dialog.getByPlaceholder(/Search/).fill('follow-ups still')
    await dialog.getByRole('button', { name: /^Open follow-ups/ }).click()

    // Only what this measure can do is offered: a count of right now has no trend.
    await expect(dialog.getByRole('button', { name: /^Trend/ })).toHaveCount(0)
    await dialog.getByRole('button', { name: /^Bars/ }).click()
    await dialog.getByRole('button', { name: 'Type', exact: true }).click()

    // A condition is picked from lists; a person is offered "Me".
    await dialog.getByRole('button', { name: /Add a Condition/ }).click()
    await expect(dialog.getByRole('combobox', { name: 'Value' })).toContainText('Me')

    // The name follows the choices until it is edited.
    const name = dialog.getByRole('textbox', { name: 'Name' })
    await expect(name).toHaveValue('Open follow-ups by type')
    await name.fill(NAME)

    await dialog.getByRole('button', { name: 'Add to Dashboard' }).click()
    await expect(page.getByText(NAME, { exact: true }).first()).toBeVisible({ timeout: 10_000 })
  })

  test('edits a widget built on a measure in the same builder', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto('/')
    const title = page.getByText(NAME, { exact: true }).first()
    await expect(title).toBeVisible({ timeout: 15_000 })
    await title.hover()
    await title.locator('xpath=ancestor::div[contains(@class, "group")][1]')
      .getByRole('button', { name: 'Edit widget' }).click({ force: true })

    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('Edit widget')).toBeVisible()
    await expect(dialog.getByText('Open follow-ups').first()).toBeVisible()
    await expect(dialog.getByRole('button', { name: /^Bars/ })).toHaveAttribute('aria-pressed', 'true')
    await dialog.getByRole('button', { name: /^Number/ }).click()
    await dialog.getByRole('button', { name: 'Save Changes' }).click()
    await expect(dialog).toBeHidden({ timeout: 10_000 })
  })
})
