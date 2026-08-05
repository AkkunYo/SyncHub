import { expect, test } from '@playwright/test'

test('connection pages expose direct search and side-panel creation flows', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/upstreams')

  await expect(page.getByRole('heading', { name: '上游连接', exact: true })).toBeVisible()
  await expect(page.getByRole('searchbox', { name: '搜索上游连接' })).toBeVisible()
  await page.locator('.connections-header').getByRole('button', { name: '添加上游连接' }).click()

  const upstreamPanel = page.getByRole('dialog', { name: '添加上游连接' })
  await expect(upstreamPanel).toBeVisible()
  await expect(upstreamPanel).toHaveClass(/side-panel/)
  await upstreamPanel.getByRole('button', { name: '关闭' }).click()
  await expect(upstreamPanel).toBeHidden()

  await page.getByRole('link', { name: '目标实例', exact: true }).click()
  await expect(page.getByRole('heading', { name: '目标实例', exact: true })).toBeVisible()
  await expect(page.getByRole('searchbox', { name: '搜索目标实例' })).toBeVisible()
  await page.locator('.connections-header').getByRole('button', { name: '添加目标实例' }).click()

  const targetPanel = page.getByRole('dialog', { name: '添加目标实例' })
  await expect(targetPanel).toBeVisible()
  await expect(targetPanel).toHaveClass(/side-panel/)
})

test('console shell keeps every primary workspace route directly addressable', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/sync')

  const topbar = page.getByRole('banner', { name: 'SyncHub 控制台顶栏' })
  const navigation = page.getByRole('navigation', { name: '主导航' })
  await expect(topbar).toBeVisible()
  await expect(page.getByLabel('本地管理 API')).toBeVisible()

  const destinations = [
    { label: '同步工作台', path: '/sync' },
    { label: '上游连接', path: '/upstreams' },
    { label: '目标实例', path: '/targets' },
    { label: '漂移修复', path: '/drift' },
    { label: '任务记录', path: '/tasks' },
    { label: '系统设置', path: '/settings' },
  ] as const

  for (const destination of destinations) {
    const link = navigation.getByRole('link', { name: destination.label, exact: true })
    await expect(link).toHaveAttribute('href', destination.path)
    await link.click()
    await expect(page).toHaveURL(new RegExp(`${destination.path}$`))
    await expect(page).toHaveTitle(`${destination.label} | SyncHub`)
    await expect(topbar.getByText(destination.label, { exact: true })).toBeVisible()
    await expect(link).toHaveAttribute('aria-current', 'page')
  }
})

test('redesigned shell remains dense and overflow-free across desktop and mobile', async ({ page }) => {
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
    { width: 320, height: 700 },
  ]) {
    await page.setViewportSize(viewport)
    await page.goto('/sync')
    await expect(page.locator('.app-shell')).toBeVisible()
    await expect(page.getByRole('banner', { name: 'SyncHub 控制台顶栏' })).toBeVisible()
    const workspaceHeading = page.getByRole('heading', { name: '资产矩阵' })
    await expect(workspaceHeading).toBeVisible()
    await expect(workspaceHeading).toContainText('同步工作台')

    const metrics = await page.evaluate(() => ({
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      headerHeight: document.querySelector('.app-header')?.getBoundingClientRect().height ?? 0,
      sidebarWidth: document.querySelector('.desktop-sidebar')?.getBoundingClientRect().width ?? 0,
      mainLeft: document.querySelector('main')?.getBoundingClientRect().left ?? 0,
      mainWidth: document.querySelector('main')?.getBoundingClientRect().width ?? 0,
      menuHeight: document.querySelector('.mobile-menu-button')?.getBoundingClientRect().height ?? 0,
    }))
    expect(metrics.overflow).toBeLessThanOrEqual(0)
    expect(metrics.headerHeight).toBe(48)
    expect(metrics.mainWidth).toBeGreaterThan(280)

    if (viewport.width > 860) {
      expect(metrics.sidebarWidth).toBeGreaterThanOrEqual(208)
      expect(metrics.sidebarWidth).toBeLessThanOrEqual(224)
      expect(metrics.mainLeft).toBe(metrics.sidebarWidth)
      const buildInfo = page.locator('.desktop-sidebar [aria-label="构建信息"]')
      await expect(buildInfo).toContainText(/版本\s+\S+/)
      await expect(buildInfo).toContainText(/编译\s+\S+/)
      continue
    }

    expect(metrics.sidebarWidth).toBe(0)
    expect(metrics.mainLeft).toBe(0)
    expect(metrics.menuHeight).toBeGreaterThanOrEqual(44)

    const menuButton = page.getByRole('button', { name: '打开导航' })
    await menuButton.click()
    await expect(page.getByRole('navigation', { name: '移动端主导航' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('navigation', { name: '移动端主导航' })).toBeHidden()
    await expect(menuButton).toBeFocused()
  }
})
