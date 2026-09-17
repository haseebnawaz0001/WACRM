import { test, expect } from '@playwright/test'
import { loginAsAdmin } from '../../helpers'
import { ContactsPage } from '../../pages'
import { createTestScope } from '../../framework'
import * as path from 'path'
import * as fs from 'fs'
import * as os from 'os'

/**
 * Contacts as its own module (plan 01).
 *
 * This replaces `tests/settings/contacts.spec.ts`, which drove the Settings
 * page that the module superseded. The behaviour it covered has not gone away,
 * it has moved:
 *
 *   - creating a contact is a header button here rather than a Settings table
 *     action, and lands on the contact's profile, which is where the record now
 *     lives
 *   - editing is the profile page, not a Settings detail form
 *   - import/export is a header button, as before
 *
 * The old path is still exercised: a redirect test lives in crm-modules.spec.ts,
 * because bookmarks and saved dashboard shortcuts still point at it.
 */

const scope = createTestScope('contacts-module')

test.describe('Contacts module', () => {
  let contactsPage: ContactsPage

  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
    contactsPage = new ContactsPage(page)
    await contactsPage.goto()
  })

  test('shows the list with its own controls', async () => {
    await contactsPage.expectPageVisible()
    await expect(contactsPage.addButton).toBeVisible()
    await expect(contactsPage.importExportButton).toBeVisible()
  })

  test('opens the create dialog', async () => {
    await contactsPage.openCreateDialog()
    await contactsPage.expectDialogVisible()
    await expect(contactsPage.dialog).toContainText(/contact/i)
  })

  test('refuses a contact with no phone number', async () => {
    await contactsPage.openCreateDialog()
    await contactsPage.submitDialog()
    await contactsPage.expectToast(/required/i)
  })

  /**
   * Creating a contact lands on its profile. Creating one is almost always the
   * first step of working on it, and sending somebody back to a list to find
   * the row they just made is a step that exists for the product's convenience
   * rather than theirs.
   */
  test('creates a contact and opens its profile', async ({ page }) => {
    const phoneNumber = scope.phone()
    const contactName = scope.name('journey')

    await contactsPage.openCreateDialog()
    await contactsPage.fillContactForm(phoneNumber, contactName)
    await contactsPage.submitDialog()

    await page.waitForURL(/\/contacts\/[a-f0-9-]+$/, { timeout: 15000 })
    await expect(page.getByText(contactName).first()).toBeVisible({ timeout: 15000 })
  })

  test('creates a contact from a phone number alone', async ({ page }) => {
    const phoneNumber = scope.phone()

    await contactsPage.openCreateDialog()
    await contactsPage.fillContactForm(phoneNumber)
    await contactsPage.submitDialog()

    await page.waitForURL(/\/contacts\/[a-f0-9-]+$/, { timeout: 15000 })
    await expect(page.getByText(phoneNumber).first()).toBeVisible({ timeout: 15000 })
  })

  test('refuses a duplicate phone number', async ({ page }) => {
    const phoneNumber = scope.phone()

    await contactsPage.openCreateDialog()
    await contactsPage.fillContactForm(phoneNumber, scope.name('first'))
    await contactsPage.submitDialog()
    await page.waitForURL(/\/contacts\/[a-f0-9-]+$/, { timeout: 15000 })

    await contactsPage.goto()
    await contactsPage.openCreateDialog()
    await contactsPage.fillContactForm(phoneNumber, scope.name('second'))
    await contactsPage.submitDialog()

    // One phone number is one person; the second attempt must not create a
    // record that would then need merging.
    await contactsPage.expectToast(/exist|duplicate|already/i)
  })

  test('searches the list', async ({ page }) => {
    const phoneNumber = scope.phone()
    const contactName = scope.name('findme')

    await contactsPage.openCreateDialog()
    await contactsPage.fillContactForm(phoneNumber, contactName)
    await contactsPage.submitDialog()
    await page.waitForURL(/\/contacts\/[a-f0-9-]+$/, { timeout: 15000 })

    await contactsPage.goto()
    await contactsPage.search(contactName)
    await expect(page.getByText(contactName).first()).toBeVisible({ timeout: 15000 })
  })

  test('cancels creation without making anything', async ({ page }) => {
    await contactsPage.openCreateDialog()
    await contactsPage.fillContactForm(scope.phone(), scope.name('cancelled'))
    await contactsPage.cancelDialog()

    await expect(contactsPage.dialog).not.toBeVisible()
    await expect(page).toHaveURL(/\/contacts$/)
  })
})

test.describe('Contacts import and export', () => {
  let contactsPage: ContactsPage

  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
    contactsPage = new ContactsPage(page)
    await contactsPage.goto()
  })

  test('opens the import/export dialog', async () => {
    await contactsPage.openImportExportDialog()
    await expect(contactsPage.importExportDialog).toBeVisible()
  })

  test('imports contacts from a CSV', async ({ page }) => {
    const phoneNumber = scope.phone()
    const contactName = scope.name('imported')

    const csv = `phone_number,profile_name\n${phoneNumber},${contactName}\n`
    const file = path.join(os.tmpdir(), `contacts-import-${Date.now()}.csv`)
    fs.writeFileSync(file, csv)

    try {
      await contactsPage.openImportExportDialog()
      await expect(contactsPage.importExportDialog).toBeVisible()

      // The import tab, then the file.
      await contactsPage.importExportDialog.getByRole('tab', { name: /import/i }).click()
      await page.setInputFiles('input[type="file"]', file)

      const importButton = contactsPage.importExportDialog.getByRole('button', { name: /^import/i })
      await expect(importButton).toBeEnabled({ timeout: 10000 })
      await importButton.click()

      // The imported contact reaches the list, which is the only proof that
      // matters — an import that reports success and lands nothing is the
      // failure worth catching.
      await contactsPage.goto()
      await contactsPage.search(phoneNumber)
      await expect(page.getByText(phoneNumber).first()).toBeVisible({ timeout: 20000 })
    } finally {
      fs.rmSync(file, { force: true })
    }
  })
})
