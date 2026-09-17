import { test, expect, request as playwrightRequest } from '@playwright/test'
import { loginAsAdmin } from '../../helpers'
import { ApiHelper } from '../../helpers/api'
import { ChatPage } from '../../pages'
import { createTestScope } from '../../framework'

const scope = createTestScope('contact-metadata')

const TEST_METADATA = {
  plan: 'premium',
  age: 30,
  active: true,
  address: {
    city: 'Mumbai',
    state: 'Maharashtra',
    zip: '400001',
  },
  orders: [
    { id: 'ORD-001', amount: 1500, status: 'delivered' },
    { id: 'ORD-002', amount: 2300, status: 'pending' },
  ],
  interests: ['fitness', 'tech', 'travel'],
}

test.describe('Contact Metadata Panel', () => {
  test.describe.configure({ mode: 'serial' }) // Tests share contact metadata state

  let contactId: string

  test.beforeAll(async () => {
    const reqContext = await playwrightRequest.newContext()
    const api = new ApiHelper(reqContext)
    await api.loginAsAdmin()

    // Get existing contacts or create one
    let contacts = await api.getContacts()
    if (contacts.length === 0) {
      await api.createContact(scope.phone(), scope.name('contact'))
      contacts = await api.getContacts()
    }

    contactId = contacts[0].id

    // Set metadata on the contact
    await api.updateContact(contactId, { metadata: TEST_METADATA })
    await reqContext.dispose()
  })

  let chatPage: ChatPage

  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
    chatPage = new ChatPage(page)
  })

  /**
   * The panel itself, not the page.
   *
   * These assertions are about what the panel shows, and a page-wide text match
   * also sees the shell around it — the notification badge is a number too, so
   * `getByText('30')` starts failing the day an agent has thirty unread.
   */
  function panel(page: import('@playwright/test').Page) {
    return page.locator('#contact-info-panel')
  }

  async function openInfoPanel(page: import('@playwright/test').Page) {
    await chatPage.goto(contactId)

    // Open contact info panel
    const infoBtn = page.locator('#info-button')
    await infoBtn.click()

    // Wait for the panel header to appear
    await expect(panel(page).getByText('Contact Info')).toBeVisible({ timeout: 10000 })
  }

  test('should display metadata section when contact has metadata', async ({ page }) => {
    await openInfoPanel(page)

    // "General" section should show for top-level primitives (plan, age, active)
    await expect(panel(page).getByText('General')).toBeVisible()

    // Verify primitive values are displayed
    await expect(panel(page).getByText('premium')).toBeVisible()
    await expect(panel(page).getByText('30', { exact: true })).toBeVisible()
  })

  test('should display nested object metadata as key-value pairs', async ({ page }) => {
    await openInfoPanel(page)

    // The metadata section's own header, by role. The organization's contact
    // fields render in the same panel and one of them is also called Address,
    // so a plain text match sees both.
    await expect(panel(page).getByRole('button', { name: 'Address' })).toBeVisible()

    // Key-value pairs inside the address section
    await expect(panel(page).getByText('Mumbai')).toBeVisible()
    await expect(panel(page).getByText('Maharashtra')).toBeVisible()
    await expect(panel(page).getByText('400001')).toBeVisible()
  })

  test('should display array of objects as a table', async ({ page }) => {
    await openInfoPanel(page)

    // "Orders" section header (formatted from "orders"), by role for the same
    // reason as Address above.
    await expect(panel(page).getByRole('button', { name: /Orders/ })).toBeVisible()

    // Table should show the count
    await expect(panel(page).getByText('(2)')).toBeVisible()

    // Table column headers
    await expect(page.locator('th').getByText('Id')).toBeVisible()
    await expect(page.locator('th').getByText('Amount')).toBeVisible()
    await expect(page.locator('th').getByText('Status')).toBeVisible()

    // Table data
    await expect(panel(page).getByText('ORD-001')).toBeVisible()
    await expect(page.getByRole('cell', { name: '1500' })).toBeVisible()
    await expect(panel(page).getByText('delivered')).toBeVisible()
    await expect(panel(page).getByText('ORD-002')).toBeVisible()
  })

  test('should display array of primitives as badges', async ({ page }) => {
    await openInfoPanel(page)

    // "Interests" section header
    await expect(panel(page).getByText('Interests')).toBeVisible()

    // Badges for each interest
    await expect(panel(page).getByText('fitness')).toBeVisible()
    await expect(panel(page).getByText('tech')).toBeVisible()
    await expect(panel(page).getByText('travel')).toBeVisible()
  })

  test('should display boolean metadata as Yes/No badges', async ({ page }) => {
    await openInfoPanel(page)

    // The "Active" label should appear in the General section
    await expect(panel(page).getByText('Active')).toBeVisible()

    // Boolean true should show as "Yes" badge
    await expect(panel(page).getByText('Yes')).toBeVisible()
  })

  test('should collapse and expand metadata sections', async ({ page }) => {
    await openInfoPanel(page)

    // Click "Address" section header to collapse it
    const addressTrigger = page.locator('button').filter({ hasText: 'Address' })
    await addressTrigger.click()

    // Values should be hidden after collapse
    await expect(panel(page).getByText('Mumbai')).toBeHidden()

    // Click again to expand
    await addressTrigger.click()

    // Values should be visible again
    await expect(panel(page).getByText('Mumbai')).toBeVisible()
  })

  test('should format metadata labels from snake_case and camelCase', async ({ page }) => {
    // Update contact with snake_case and camelCase keys
    const reqContext = await playwrightRequest.newContext()
    const api = new ApiHelper(reqContext)
    await api.loginAsAdmin()
    await api.updateContact(contactId, {
      metadata: {
        first_name: 'John',
        lastName: 'Doe',
      },
    })
    await reqContext.dispose()

    await openInfoPanel(page)

    // snake_case "first_name" should become "First Name"
    await expect(panel(page).getByText('First Name')).toBeVisible()

    // camelCase "lastName" should become "Last Name"
    await expect(panel(page).getByText('Last Name')).toBeVisible()

    // Restore original metadata for other tests
    const restoreCtx = await playwrightRequest.newContext()
    const restoreApi = new ApiHelper(restoreCtx)
    await restoreApi.loginAsAdmin()
    await restoreApi.updateContact(contactId, { metadata: TEST_METADATA })
    await restoreCtx.dispose()
  })

  test('should not show metadata section when contact has no metadata', async ({ page }) => {
    // Clear metadata
    const reqContext = await playwrightRequest.newContext()
    const api = new ApiHelper(reqContext)
    await api.loginAsAdmin()
    await api.updateContact(contactId, { metadata: {} })
    await reqContext.dispose()

    await openInfoPanel(page)

    // "General" section from metadata should not be visible
    await expect(panel(page).getByText('General')).toBeHidden()

    // Restore metadata
    const restoreCtx = await playwrightRequest.newContext()
    const restoreApi = new ApiHelper(restoreCtx)
    await restoreApi.loginAsAdmin()
    await restoreApi.updateContact(contactId, { metadata: TEST_METADATA })
    await restoreCtx.dispose()
  })
})
