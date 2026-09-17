// Screenshots of every console screen for visual review, in light and dark at
// three sizes. Written to e2e/screenshots/ (git-ignored); they assert only
// that each screen rendered.
import { join } from 'node:path'

import { test } from './fixtures'

import { endAllSessions, ovdb, port, primary, signedInContext } from './ovdb'

const sizes = [
  { width: 1280, height: 800 },
  { width: 1920, height: 1080 },
  { width: 360, height: 740 },
]
const schemes = ['light', 'dark'] as const
const screens = [
  { name: 'home', path: '/', ready: 'What would you like to do?' },
  { name: 'server', path: '/server', ready: 'Log file' },
  { name: 'settings', path: '/settings', ready: 'Save port' },
  { name: 'create', path: '/databases/new', ready: 'Where should OVDB keep your data?' },
  { name: 'databases', path: '/databases', ready: 'Databases' },
]
const out = (name: string) => join(import.meta.dirname, 'screenshots', name + '.png')

for (const size of sizes) {
  for (const colorScheme of schemes) {
    const suffix = `${size.width}x${size.height}-${colorScheme}`
    test(`screens ${suffix}`, async ({ browser }) => {
      const context = await signedInContext(browser, { viewport: size, colorScheme })
      const page = await context.newPage()
      for (const screen of screens) {
        await page.goto(primary() + screen.path)
        await page.getByText(screen.ready, { exact: true }).first().waitFor()
        await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
        await page.screenshot({ path: out(`${screen.name}-${suffix}`), fullPage: true })
      }

      // Create: the filtered picker, PostgreSQL's manifest steps, the form,
      // a SQLite Result, a refusal, and Databases with a confirmation.
      const name = `shot-${size.width}-${colorScheme}`
      await page.goto(primary() + '/databases/new')
      await page.getByLabel('Filter').fill('sql')
      await page.screenshot({ path: out(`create-filter-${suffix}`), fullPage: true })
      await page.locator('[data-engine="postgres"]').click()
      await page.getByTestId('manifest-steps').waitFor()
      await page.screenshot({ path: out(`create-postgres-${suffix}`), fullPage: true })
      await page.getByRole('button', { name: 'Change storage' }).click()
      await page.locator('[data-engine="sqlite"]').click()
      await page.getByLabel('Name').fill(name)
      await page.screenshot({ path: out(`create-form-${suffix}`), fullPage: true })
      await page.getByRole('button', { name: 'Create database' }).click()
      await page.getByTestId('create-result').waitFor()
      await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
      await page.screenshot({ path: out(`create-result-${suffix}`), fullPage: true })
      await page.goto(primary() + '/databases/new')
      await page.locator('[data-engine="sqlite"]').click()
      await page.getByLabel('Name').fill(name)
      await page.getByRole('button', { name: 'Create database' }).click()
      await page.getByRole('alert').waitFor()
      await page.screenshot({ path: out(`create-refused-${suffix}`), fullPage: true })
      await page.goto(primary() + '/databases')
      await page.locator(`[data-remove="${name}"]`).click()
      await page.screenshot({ path: out(`databases-confirm-${suffix}`), fullPage: true })
      ovdb('databases', 'remove', name, '--yes')

      // Settings after saving a new port, then after an invalid one.
      await page.goto(primary() + '/settings')
      await page.getByLabel('Port').fill(String(port() + 1))
      await page.getByRole('button', { name: 'Save port' }).click()
      await page.getByRole('status').waitFor()
      await page.screenshot({ path: out(`settings-saved-${suffix}`), fullPage: true })
      ovdb('config', 'set', 'server.port', String(port()))
      await page.goto(primary() + '/settings')
      await page.getByLabel('Port').fill('70000')
      await page.getByRole('button', { name: 'Save port' }).click()
      await page.getByRole('alert').waitFor()
      await page.screenshot({ path: out(`settings-invalid-${suffix}`), fullPage: true })

      // The session ends while the page is open.
      await page.goto(primary() + '/server')
      await page.getByText('Log file').waitFor()
      endAllSessions()
      await page.evaluate(() => window.dispatchEvent(new Event('focus')))
      await page.getByTestId('session-ended').waitFor()
      await page.screenshot({ path: out(`session-ended-${suffix}`), fullPage: true })
      await context.close()

      // The server stops while the page is open.
      const stopping = await signedInContext(browser, { viewport: size, colorScheme })
      const stopped = await stopping.newPage()
      await stopped.goto(primary() + '/')
      await stopped.getByText('What would you like to do?').waitFor()
      await stopped.route('**/api/local/v1/**', (route) => route.abort('connectionrefused'))
      await stopped.evaluate(() => window.dispatchEvent(new Event('focus')))
      await stopped.getByTestId('server-stopped').waitFor()
      await stopped.screenshot({ path: out(`server-stopped-${suffix}`), fullPage: true })
      await stopping.close()

      const anonymous = await browser.newContext({ viewport: size, colorScheme })
      const landing = await anonymous.newPage()
      await landing.goto(primary() + '/')
      await landing.getByText('Open the console from OVDB').first().waitFor()
      await landing.screenshot({ path: out(`landing-${suffix}`), fullPage: true })
      await landing.goto(primary() + '/signed-out')
      await landing.getByText('You signed out').waitFor()
      await landing.screenshot({ path: out(`signed-out-${suffix}`), fullPage: true })
      await anonymous.close()
    })
  }
}
