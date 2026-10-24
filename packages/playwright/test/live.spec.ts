import { test, expect } from '@agentworks/playwright'

test('live checkout demo', async ({ page, context }) => {
  await page.setContent('<body style="background:#123456;color:white"><input id="human" style="position:absolute;left:8px;top:80px"><h1>Checkout test</h1><button onclick="document.querySelector(\'h1\').textContent=\'Order confirmed\';document.body.style.background=\'#285430\'">Place order</button></body>')
  await expect(page.getByRole('heading')).toHaveText('Checkout test')
  await expect(page.locator('#human')).toHaveValue('manualx')
  // Deliberate visual holds so the integration harness can observe both states.
  await page.waitForTimeout(1500)
  await page.getByRole('button', { name: 'Place order' }).click()
  await expect(page.getByRole('heading')).toHaveText('Order confirmed')
  await page.waitForTimeout(1500)
  const popup = await context.newPage()
  await popup.setContent('<body style="background:#cca020"><h1>Popup</h1></body>')
  await page.waitForTimeout(1500)
  await popup.close()
  await page.waitForTimeout(1500)
})
