import { expect, test, type Locator, type Page } from '@playwright/test'

const fixtureURL = 'http://127.0.0.1:19090'

async function addInstance(
  page: Page,
  kind: '目标' | '上游',
  input: {
    id: string
    name: string
    baseURL: string
    userId: number
    credentialLabel: string
    credential: string
  },
): Promise<void> {
  await page.getByRole('button', { name: `添加${kind}实例` }).click()
  const dialog = page.getByRole('dialog', { name: `添加${kind}实例` })
  await dialog.getByLabel('实例 ID').fill(input.id)
  await dialog.getByLabel('名称').fill(input.name)
  await dialog.getByLabel('Base URL').fill(input.baseURL)
  await dialog.getByLabel('New API 用户 ID').fill(String(input.userId))
  await dialog.getByLabel(input.credentialLabel).fill(input.credential)
  await dialog.getByRole('button', { name: `保存${kind}实例` }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText(input.name, { exact: true })).toBeVisible()
}

async function expectNoOverlap(first: Locator, second: Locator): Promise<void> {
  const [firstBox, secondBox] = await Promise.all([first.boundingBox(), second.boundingBox()])
  expect(firstBox).not.toBeNull()
  expect(secondBox).not.toBeNull()
  if (!firstBox || !secondBox) return
  const separated = firstBox.x + firstBox.width <= secondBox.x || secondBox.x + secondBox.width <= firstBox.x ||
    firstBox.y + firstBox.height <= secondBox.y || secondBox.y + secondBox.height <= firstBox.y
  expect(separated).toBe(true)
}

test('administrator completes the live multi-target synchronization and reconciliation journey', async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '资产矩阵' })).toBeVisible()
  await page.getByRole('button', { name: '设置', exact: true }).click()

  await addInstance(page, '上游', {
    id: 'source-e2e',
    name: 'E2E New API Source',
    baseURL: `${fixtureURL}/source`,
    userId: 9001,
    credentialLabel: '访问令牌',
    credential: 'E2E_SOURCE_ADMIN_TOKEN_PLACEHOLDER',
  })
  await addInstance(page, '目标', {
    id: 'target-a',
    name: 'E2E Target Alpha',
    baseURL: `${fixtureURL}/target-a`,
    userId: 9101,
    credentialLabel: '访问令牌',
    credential: 'E2E_TARGET_A_ADMIN_TOKEN_PLACEHOLDER',
  })
  await addInstance(page, '目标', {
    id: 'target-b',
    name: 'E2E Target Beta',
    baseURL: `${fixtureURL}/target-b`,
    userId: 9102,
    credentialLabel: '访问令牌',
    credential: 'E2E_TARGET_B_ADMIN_TOKEN_PLACEHOLDER',
  })

  await page.getByRole('button', { name: '资产矩阵' }).click()
  await page.locator('.workspace-toolbar').getByRole('button', { name: '刷新资产' }).click()
  await expect(page.getByText('E2E upstream key', { exact: true })).toBeVisible()
  await page.getByRole('checkbox', { name: '选择资产 E2E upstream key' }).check()
  await page.getByRole('button', { name: '批量同步 1 个资产' }).click()
  const syncDialog = page.getByRole('dialog', { name: '批量同步设置' })
  await syncDialog.getByRole('button', { name: '开始同步' }).click()
  await expect(syncDialog.getByRole('heading', { name: '同步完成' })).toBeVisible()
  await expect(syncDialog.getByText('#101')).toBeVisible()
  await expect(syncDialog.getByText('#201')).toBeVisible()
  await expect(syncDialog.getByLabel('一次性安全证明')).toHaveValue('')
  await syncDialog.getByRole('button', { name: '关闭', exact: true }).click()
  await expect(page.locator('.matrix-table .status-synced')).toHaveCount(2)

  const driftResponse = await request.post(`${fixtureURL}/__control/target-a/channels/101/drift`)
  expect(driftResponse.ok()).toBe(true)
  await page.getByRole('button', { name: '配置漂移' }).click()
  await page.getByRole('button', { name: '校验全部目标' }).click()
  await expect(page.getByText('100 -> 61')).toBeVisible()
  await page.getByRole('button', { name: '接受 E2E upstream key 在 E2E Target Alpha 的目标端状态' }).click()
  await expect(page.getByText('漂移已接受')).toBeVisible()

  const deleteResponse = await request.delete(`${fixtureURL}/__control/target-b/channels/201`)
  expect(deleteResponse.ok()).toBe(true)
  await page.getByRole('button', { name: '校验全部目标' }).click()
  await page.getByRole('button', { name: '资产矩阵' }).click()
  await expect(page.locator('.matrix-table .status-synced')).toHaveCount(1)
  await expect(page.locator('.matrix-table .status-unsynced')).toHaveCount(1)

  await page.getByRole('button', { name: '目标渠道' }).click()
  await expect(page.getByText('E2E upstream key', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '编辑渠道 E2E upstream key' }).click()
  const editDialog = page.getByRole('dialog', { name: '编辑目标渠道' })
  await editDialog.getByLabel('名称').fill('E2E managed updated')
  await editDialog.getByLabel('模型').fill('gpt-4.1, gpt-4.1-mini')
  await editDialog.getByLabel('分组').fill('e2e')
  await editDialog.getByLabel('优先级').fill('2')
  await editDialog.getByLabel('权重').fill('70')
  await editDialog.getByRole('button', { name: '保存渠道' }).click()
  await expect(page.getByText('E2E managed updated', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: '资产矩阵' }).click()
  await expect(page.locator('.matrix-table .status-synced')).toHaveCount(1)
  await expect(page.locator('.matrix-table .status-unsynced')).toHaveCount(1)
  await page.getByRole('button', { name: '目标渠道' }).click()
  await page.getByRole('button', { name: '删除渠道 E2E managed updated' }).click()
  await page.getByRole('dialog', { name: '删除目标渠道' }).getByRole('button', { name: '确认删除' }).click()
  await page.getByRole('button', { name: '资产矩阵' }).click()
  await expect(page.locator('.matrix-table .status-unsynced')).toHaveCount(2)

  for (const width of [320, 390, 1440]) {
    await page.setViewportSize({ width, height: width < 600 ? 760 : 1000 })
    await expect(page.getByRole('heading', { name: '资产矩阵' })).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    await expectNoOverlap(
      page.getByLabel('上游实例'),
      page.locator('.workspace-toolbar').getByRole('button', { name: '刷新资产' }),
    )
  }

  await page.setViewportSize({ width: 320, height: 760 })
  const menuButton = page.getByRole('button', { name: '打开导航' })
  await menuButton.click()
  await expect(page.getByRole('navigation', { name: '移动端主导航' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('navigation', { name: '移动端主导航' })).toBeHidden()
  await expect(menuButton).toBeFocused()
})
