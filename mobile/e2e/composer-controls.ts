import type { Page } from "@playwright/test"

export async function activateComposer(page: Page) {
  await page.getByTestId("message-composer").locator("textarea").tap()
}
