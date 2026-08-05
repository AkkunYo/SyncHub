import { expect, test } from '@playwright/test'

test('connection pages expose direct search and side-panel creation flows', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/upstreams')

  await expect(page.getByRole('heading', { name: '上游连接', exact: true })).toBeVisible()
  await expect(page.getByRole('searchbox', { name: '搜索上游连接' })).toBeVisible()
  await page.getByRole('button', { name: '添加上游连接' }).click()

  const upstreamPanel = page.getByRole('dialog', { name: '添加上游连接' })
  await expect(upstreamPanel).toBeVisible()
  await expect(upstreamPanel).toHaveClass(/side-panel/)
  await upstreamPanel.getByRole('button', { name: '关闭' }).click()
  await expect(upstreamPanel).toBeHidden()

  await page.getByRole('link', { name: '目标实例', exact: true }).click()
  await expect(page.getByRole('heading', { name: '目标实例', exact: true })).toBeVisible()
  await expect(page.getByRole('searchbox', { name: '搜索目标实例' })).toBeVisible()
  await page.getByRole('button', { name: '添加目标实例' }).click()

  const targetPanel = page.getByRole('dialog', { name: '添加目标实例' })
  await expect(targetPanel).toBeVisible()
  await expect(targetPanel).toHaveClass(/side-panel/)
})

test('redesigned shell remains dense and overflow-free across desktop and mobile', async ({ page }) => {
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
    { width: 320, height: 700 },
  ]) {
    await page.setViewportSize(viewport)
    await page.goto('/sync')
    await expect(page.getByRole('heading', { name: '资产矩阵' })).toBeVisible()

    const metrics = await page.evaluate(() => ({
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      headerHeight: document.querySelector('.app-header')?.getBoundingClientRect().height ?? 0,
      mainWidth: document.querySelector('main')?.getBoundingClientRect().width ?? 0,
    }))
    expect(metrics.overflow).toBeLessThanOrEqual(0)
    expect(metrics.headerHeight).toBe(48)
    expect(metrics.mainWidth).toBeGreaterThan(280)
  }
})
