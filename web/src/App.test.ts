import { createPinia } from 'pinia'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createWebHistory } from 'vue-router'

import App from './App.vue'
import { createAppRouter } from './router'
import type { MatrixRow, UpstreamAsset } from './types'

type RouteHandler = (url: URL, init: RequestInit) => Response | Promise<Response>

const targetA = {
  id: 'target-a',
  name: 'Target Alpha',
  type: 'newapi',
  base_url: 'https://target-a.invalid',
  validation_status: 'verified',
}

const targetB = {
  id: 'target-b',
  name: 'Target Beta',
  type: 'cliproxyapi',
  base_url: 'https://target-b.invalid',
  validation_status: 'verified',
}

const upstream = {
  id: 'source-a',
  name: 'Source Alpha',
  type: 'newapi',
  base_url: 'https://source.invalid',
  sync_mappings: [],
}

const config = {
  app: {
    host: '127.0.0.1',
    port: 8888,
    reconcile_interval: '5m0s',
    request_timeout: '15s',
    sync_concurrency: 4,
  },
  targets: [targetA, targetB],
  upstreams: [upstream],
}

const assetOne: UpstreamAsset = {
  id: 'source-a:channel:7:key:0',
  source_id: 'source-a',
  source_type: 'newapi',
  provider: 'openai',
  raw_type: 'OpenAI',
  kind: 'static_api_key',
  name: 'OpenAI primary',
  base_url: 'https://provider.invalid',
  models: ['gpt-4.1'],
  enabled: true,
  secret_readable: true,
  metadata: {},
}

const assetTwo: UpstreamAsset = {
  ...assetOne,
  id: 'source-a:channel:8:key:0',
  provider: 'anthropic',
  raw_type: 'Anthropic',
  name: 'Claude reserve',
  models: ['claude-sonnet-4'],
}

function envelope(data: unknown, requestId = 'req-test'): Response {
  return new Response(JSON.stringify({ success: true, data, request_id: requestId }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function failure(code: string, message: string, status = 400): Response {
  return new Response(
    JSON.stringify({ success: false, error: { code, message }, request_id: 'req-error' }),
    { status, headers: { 'Content-Type': 'application/json' } },
  )
}

function matrix(rows: MatrixRow[] = [
  {
    asset: assetOne,
    cells: [
      { target_id: 'target-a', status: 'unsynced' },
      { target_id: 'target-b', status: 'unsynced' },
    ],
  },
]) {
  return {
    upstream_id: 'source-a',
    refreshed: true,
    targets: [targetA, targetB],
    rows,
  }
}

function installFetch(handler: RouteHandler) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const path = typeof input === 'string' ? input : input.toString()
    return handler(new URL(path, 'http://synchub.local'), init)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

function installConsoleApi(matrixData = matrix()) {
  return installFetch((url, init) => {
    if (url.pathname === '/api/v1/health') {
      return envelope({ status: 'ok', version: 'v1.3.0', build_date: '2026-07-29T16:00:00+08:00' })
    }
    if (url.pathname === '/api/v1/config') return envelope(config)
    if (url.pathname === '/api/v1/matrix' && url.searchParams.get('upstream_id') === 'source-a') {
      return envelope(matrixData)
    }
    throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}${url.search}`)
  })
}

function renderApp() {
  const router = createAppRouter(createWebHistory())
  return {
    ...render(App, { global: { plugins: [createPinia(), router] } }),
    router,
  }
}

async function openTargetChannels(user: ReturnType<typeof userEvent.setup>, targetName = 'Target Alpha') {
  const navigation = screen.getByRole('navigation', { name: '主导航' })
  await user.click(within(navigation).getByRole('link', { name: '目标实例' }))
  await user.click(screen.getByRole('link', { name: `查看 ${targetName} 概览` }))
  await user.click(screen.getByRole('link', { name: '查看渠道' }))
}

describe('SyncHub console', () => {
  beforeEach(() => {
    vi.stubGlobal('scrollTo', vi.fn())
    window.localStorage.clear()
    window.sessionStorage.clear()
    window.history.replaceState({}, '', '/')
  })

  it('shows a stable loading state before rendering the live asset matrix', async () => {
    let releaseConfig: ((response: Response) => void) | undefined
    const pendingConfig = new Promise<Response>((resolve) => {
      releaseConfig = resolve
    })
    installFetch((url) => {
      if (url.pathname === '/api/v1/config') return pendingConfig
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      throw new Error(`Unexpected request: ${url.pathname}`)
    })

    renderApp()

    expect(screen.getByRole('status', { name: '正在加载控制台' })).toBeInTheDocument()
    releaseConfig?.(envelope(config))

    expect(await screen.findByRole('heading', { name: '资产矩阵' })).toBeInTheDocument()
    expect(await screen.findByText('OpenAI primary')).toBeInTheDocument()
    const matrixTable = document.querySelector<HTMLElement>('.matrix-table')
    expect(matrixTable).not.toBeNull()
    expect(within(matrixTable!).getAllByText('未同步')).toHaveLength(2)
  })

  it('routes the fixed console navigation through stable URLs', async () => {
    installConsoleApi()
    const user = userEvent.setup()

    renderApp()

    const navigation = await screen.findByRole('navigation', { name: '主导航' })
    const destinations = [
      ['同步工作台', '/sync'],
      ['上游连接', '/upstreams'],
      ['目标实例', '/targets'],
      ['漂移修复', '/drift'],
      ['任务记录', '/tasks'],
      ['系统设置', '/settings'],
    ] as const

    await waitFor(() => expect(window.location.pathname).toBe('/sync'))
    for (const [label, path] of destinations) {
      expect(within(navigation).getByRole('link', { name: label })).toHaveAttribute('href', path)
    }

    await user.click(within(navigation).getByRole('link', { name: '任务记录' }))
    expect(window.location.pathname).toBe('/tasks')
    expect(screen.getByRole('heading', { name: '任务记录' })).toBeInTheDocument()

    await user.click(within(navigation).getByRole('link', { name: '漂移修复' }))
    expect(window.location.pathname).toBe('/drift')
    expect(screen.getByRole('heading', { name: '配置漂移' })).toBeInTheDocument()
    expect(within(navigation).getByRole('link', { name: '漂移修复' })).toHaveAttribute('aria-current', 'page')
  })

  it('collapses the desktop navigation and restores the saved workspace preference', async () => {
    installConsoleApi()
    const user = userEvent.setup()
    const firstRender = renderApp()

    const collapseButton = await screen.findByRole('button', { name: '收起导航' })
    const shell = document.querySelector('.app-shell')
    expect(shell).not.toHaveClass('sidebar-collapsed')

    await user.click(collapseButton)
    expect(shell).toHaveClass('sidebar-collapsed')
    expect(collapseButton).toHaveAccessibleName('展开导航')
    expect(window.localStorage.getItem('synchub.sidebar.collapsed')).toBe('true')

    firstRender.unmount()
    renderApp()

    expect(await screen.findByRole('button', { name: '展开导航' })).toBeInTheDocument()
    expect(document.querySelector('.app-shell')).toHaveClass('sidebar-collapsed')
  })

  it('refreshes the console from the global header without changing routes', async () => {
    const fetchMock = installConsoleApi()
    const user = userEvent.setup()
    const { router } = renderApp()

    await router.push('/targets')
    expect(await screen.findByRole('heading', { name: '目标实例' })).toBeInTheDocument()
    const configCallsBeforeRefresh = fetchMock.mock.calls
      .filter(([input]) => String(input).includes('/api/v1/config')).length

    await user.click(screen.getByRole('button', { name: '刷新控制台' }))

    await waitFor(() => {
      const configCallsAfterRefresh = fetchMock.mock.calls
        .filter(([input]) => String(input).includes('/api/v1/config')).length
      expect(configCallsAfterRefresh).toBe(configCallsBeforeRefresh + 1)
    })
    expect(window.location.pathname).toBe('/targets')
  })

  it('keeps every documented detail URL addressable', async () => {
    installConsoleApi()
    const { router } = renderApp()

    await screen.findByText('OpenAI primary')
    const destinations = [
      { path: '/upstreams/source-a', heading: '上游详情', navigation: '上游连接' },
      { path: '/targets/target-a', heading: '目标概览', navigation: '目标实例' },
      { path: '/drift/finding-a', heading: '漂移详情', navigation: '漂移修复' },
      { path: '/tasks/task-a', heading: '任务详情', navigation: '任务记录' },
    ] as const

    for (const destination of destinations) {
      await router.push(destination.path)
      expect(window.location.pathname).toBe(destination.path)
      expect(screen.getByRole('heading', { name: destination.heading })).toBeInTheDocument()
      expect(
        within(screen.getByRole('navigation', { name: '主导航' }))
          .getByRole('link', { name: destination.navigation }),
      ).toHaveAttribute('aria-current', 'page')
      expect(document.title).toBe(`${destination.heading} | SyncHub`)
    }
  })

  it('opens resource details from the upstream and target lists', async () => {
    installConsoleApi()
    const user = userEvent.setup()

    renderApp()

    const navigation = await screen.findByRole('navigation', { name: '主导航' })
    await user.click(within(navigation).getByRole('link', { name: '上游连接' }))
    const upstreamDetails = screen.getByRole('link', { name: '查看 Source Alpha 详情' })
    expect(upstreamDetails).toHaveAttribute('href', '/upstreams/source-a')
    await user.click(upstreamDetails)
    expect(screen.getByRole('heading', { name: '上游详情' })).toBeInTheDocument()

    await user.click(within(navigation).getByRole('link', { name: '目标实例' }))
    const targetOverview = screen.getByRole('link', { name: '查看 Target Alpha 概览' })
    expect(targetOverview).toHaveAttribute('href', '/targets/target-a')
    await user.click(targetOverview)
    expect(screen.getByRole('heading', { name: '目标概览' })).toBeInTheDocument()
  })

  it('keeps unknown resource URLs recoverable when configuration is empty', async () => {
    installFetch((url) => {
      if (url.pathname === '/api/v1/health') {
        return envelope({ status: 'ok', version: 'v1.3.0', build_date: '2026-07-29T16:00:00+08:00' })
      }
      if (url.pathname === '/api/v1/config') {
        return envelope({ ...config, targets: [], upstreams: [] })
      }
      throw new Error(`Unexpected request: ${url.pathname}`)
    })
    const user = userEvent.setup()
    const { router } = renderApp()

    await screen.findByRole('heading', { name: '资产矩阵' })
    await router.push('/upstreams/missing-upstream')
    expect(window.location.pathname).toBe('/upstreams/missing-upstream')
    expect(screen.getByRole('heading', { name: '未找到对应实例' })).toBeInTheDocument()
    await user.click(screen.getByRole('link', { name: '返回列表' }))
    expect(screen.getByRole('heading', { name: '尚未配置上游连接' })).toBeInTheDocument()

    await router.push('/targets/missing-target')
    expect(window.location.pathname).toBe('/targets/missing-target')
    expect(screen.getByRole('heading', { name: '未找到对应实例' })).toBeInTheDocument()
    await user.click(screen.getByRole('link', { name: '返回列表' }))
    expect(screen.getByRole('heading', { name: '尚未配置目标实例' })).toBeInTheDocument()
  })

  it('loads the target selected by a direct channel route', async () => {
    const fetchMock = installFetch((url) => {
      if (url.pathname === '/api/v1/health') {
        return envelope({ status: 'ok', version: 'v1.3.0', build_date: '2026-07-29T16:00:00+08:00' })
      }
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/targets/target-b/channels') {
        return envelope({
          channels: [{
            id: '77',
            name: 'Target Beta channel',
            provider: 'openai',
            raw_type: '1',
            base_url: 'https://provider.invalid',
            models: ['gpt-4.1'],
            group: 'default',
            priority: 0,
            weight: 100,
            enabled: true,
            managed: false,
          }],
        })
      }
      throw new Error(`Unexpected request: ${url.pathname}`)
    })
    window.history.replaceState({}, '', '/targets/target-b/channels')

    renderApp()

    expect(await screen.findByText('Target Beta channel')).toBeInTheDocument()
    expect(screen.getByLabelText('目标实例')).toHaveValue('target-b')
    expect(within(screen.getByRole('navigation', { name: '主导航' })).getByRole('link', { name: '目标实例' }))
      .toHaveAttribute('aria-current', 'page')
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/targets/target-b/channels'))).toBe(true)
  })

  it('keeps the active route and the single page heading synchronized', async () => {
    installFetch((url) => {
      if (url.pathname === '/api/v1/health') {
        return envelope({ status: 'ok', version: 'v1.3.0', build_date: '2026-07-29T16:00:00+08:00' })
      }
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      throw new Error(`Unexpected request: ${url.pathname}`)
    })
    const user = userEvent.setup()

    renderApp()

    const topbar = await screen.findByRole('banner', { name: 'SyncHub 控制台顶栏' })
    expect(within(topbar).getByRole('link', { name: 'SyncHub 首页' })).toHaveAttribute('href', '/sync')
    expect(within(topbar).getByRole('status', { name: '本地管理 API' })).toHaveTextContent('最近检查正常')
    expect(within(topbar).getByText('同步工作台')).toBeInTheDocument()

    const sidebar = screen.getByRole('complementary', { name: '控制台导航' })
    const navigation = within(sidebar).getByRole('navigation', { name: '主导航' })
    const buildInfo = within(sidebar).getByRole('contentinfo', { name: '构建信息' })
    expect(buildInfo).toHaveTextContent('版本 v1.3.0')
    expect(buildInfo).toHaveTextContent('编译 2026-07-29 16:00:00')
    const main = screen.getByRole('main')
    const expectPage = async (navigationLabel: string, heading: string) => {
      await waitFor(() => {
        const activeItems = within(navigation)
          .getAllByRole('link')
          .filter((link) => link.getAttribute('aria-current') === 'page')
        expect(activeItems).toHaveLength(1)
        expect(activeItems[0]).toHaveAccessibleName(navigationLabel)
        const pageHeadings = within(main).getAllByRole('heading', { level: 1 })
        expect(pageHeadings).toHaveLength(1)
        expect(pageHeadings[0]).toHaveTextContent(heading)
        expect(within(topbar).getByText(navigationLabel)).toBeInTheDocument()
      })
    }

    await expectPage('同步工作台', '资产矩阵')
    await user.click(within(navigation).getByRole('link', { name: '上游连接' }))
    await expectPage('上游连接', '上游连接')
    await user.click(within(navigation).getByRole('link', { name: '目标实例' }))
    await expectPage('目标实例', '目标实例')
    await user.click(within(navigation).getByRole('link', { name: '漂移修复' }))
    await expectPage('漂移修复', '配置漂移')
    await user.click(within(navigation).getByRole('link', { name: '任务记录' }))
    await expectPage('任务记录', '任务记录')
    await user.click(within(navigation).getByRole('link', { name: '系统设置' }))
    await expectPage('系统设置', '设置')
  })

  it('shows the running binary version and build time', async () => {
    installFetch((url) => {
      if (url.pathname === '/api/v1/health') {
        return envelope({ status: 'ok', version: 'v1.3.0', build_date: '2026-07-29T16:00:00+08:00' })
      }
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      throw new Error(`Unexpected request: ${url.pathname}`)
    })

    renderApp()

    const sidebar = await screen.findByRole('complementary', { name: '控制台导航' })
    const buildInfo = within(sidebar).getByRole('contentinfo', { name: '构建信息' })
    expect(within(buildInfo).getByText('版本 v1.3.0')).toBeInTheDocument()
    expect(within(buildInfo).getByText('编译 2026-07-29 16:00:00')).toBeInTheDocument()
    expect(within(screen.getByRole('status', { name: '本地管理 API' })).getByText('最近检查正常'))
      .toBeInTheDocument()
  })

  it('reports an unknown API status when the health check fails', async () => {
    installFetch((url) => {
      if (url.pathname === '/api/v1/health') return failure('upstream_failure', '健康检查失败', 503)
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      throw new Error(`Unexpected request: ${url.pathname}`)
    })

    renderApp()

    const apiStatus = await screen.findByLabelText('本地管理 API')
    expect(apiStatus).toHaveTextContent('状态未知')
    expect(within(apiStatus).queryByText('最近检查正常')).not.toBeInTheDocument()
  })

  it('keeps the console available when the initial matrix request fails', async () => {
    installFetch((url) => {
      if (url.pathname === '/api/v1/health') {
        return envelope({ status: 'ok', version: 'v1.3.0', build_date: '2026-07-29T16:00:00+08:00' })
      }
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') {
        return failure('upstream_failure', '暂时无法读取资产矩阵', 502)
      }
      throw new Error(`Unexpected request: ${url.pathname}`)
    })
    const user = userEvent.setup()

    renderApp()

    expect(await screen.findByRole('banner', { name: 'SyncHub 控制台顶栏' })).toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: '资产矩阵' })).toBeInTheDocument()
    expect(await screen.findByRole('alert')).toHaveTextContent('暂时无法读取资产矩阵')
    const navigation = screen.getByRole('navigation', { name: '主导航' })
    await user.click(within(navigation).getByRole('link', { name: '漂移修复' }))
    expect(screen.getByRole('heading', { name: '配置漂移' })).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('暂时无法读取资产矩阵')
    expect(screen.queryByText('当前没有配置漂移')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeEnabled()

    await user.click(within(navigation).getByRole('link', { name: '系统设置' }))
    expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument()
  })

  it('provides safe error, retry, and empty states', async () => {
    let configAttempts = 0
    installFetch((url) => {
      if (url.pathname === '/api/v1/config') {
        configAttempts += 1
        return configAttempts === 1
          ? failure('upstream_failure', '暂时无法读取配置', 502)
          : envelope({ ...config, upstreams: [] })
      }
      throw new Error(`Unexpected request: ${url.pathname}`)
    })

    renderApp()

    expect(await screen.findByRole('alert')).toHaveTextContent('暂时无法读取配置')
    await fireEvent.click(screen.getByRole('button', { name: '重试加载控制台' }))

    expect(await screen.findByText('尚未配置上游实例')).toBeInTheDocument()
    const configureUpstream = screen.getByRole('link', { name: '配置上游连接' })
    expect(configureUpstream).toHaveAttribute('href', '/upstreams')
    await userEvent.setup().click(configureUpstream)
    expect(await screen.findByRole('heading', { name: '上游连接' })).toBeInTheDocument()
    expect(window.location.pathname).toBe('/upstreams')
  })

  it('filters matrix rows locally and only shows bulk actions for a selection', async () => {
    installConsoleApi(
      matrix([
        {
          asset: assetOne,
          cells: [
            { target_id: 'target-a', status: 'unsynced' },
            { target_id: 'target-b', status: 'unsynced' },
          ],
        },
        {
          asset: assetTwo,
          cells: [
            { target_id: 'target-a', status: 'synced', channel_id: '42' },
            { target_id: 'target-b', status: 'synced', channel_id: '93' },
          ],
        },
      ]),
    )
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    expect(screen.getByText('Claude reserve')).toBeInTheDocument()
    expect(screen.queryByRole('toolbar', { name: '批量操作' })).not.toBeInTheDocument()

    const assetSearch = screen.getByRole('searchbox', { name: '搜索资产' })
    await user.type(assetSearch, 'claude')
    expect(screen.queryByText('OpenAI primary')).not.toBeInTheDocument()
    expect(screen.getByText('Claude reserve')).toBeInTheDocument()

    await user.clear(assetSearch)
    await user.selectOptions(screen.getByRole('combobox', { name: '同步状态' }), 'unsynced')
    expect(screen.getByText('OpenAI primary')).toBeInTheDocument()
    expect(screen.queryByText('Claude reserve')).not.toBeInTheDocument()

    await user.click(screen.getByRole('checkbox', { name: '选择资产 OpenAI primary' }))
    const bulkActions = screen.getByRole('toolbar', { name: '批量操作' })
    expect(within(bulkActions).getByRole('button', { name: '批量同步 1 个资产' })).toBeEnabled()
    await user.click(within(bulkActions).getByRole('button', { name: '清除选择' }))
    expect(screen.queryByRole('toolbar', { name: '批量操作' })).not.toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: '选择资产 OpenAI primary' })).not.toBeChecked()
  })

  it('multi-selects assets and reports each target in a partial sync result', async () => {
    const fetchMock = installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') {
        return envelope(
          matrix([
            {
              asset: assetOne,
              cells: [
                { target_id: 'target-a', status: 'unsynced' },
                { target_id: 'target-b', status: 'unsynced' },
              ],
            },
            {
              asset: assetTwo,
              cells: [
                { target_id: 'target-a', status: 'unsynced' },
                { target_id: 'target-b', status: 'unsynced' },
              ],
            },
          ]),
        )
      }
      if (url.pathname === '/api/v1/sync' && init.method === 'POST') {
        return envelope({
          units: [
            { unit_id: 'sync-1', asset_id: assetOne.id, target_id: 'target-a', status: 'synced', channel_id: '42', effective_models: ['gpt-4.1'], excluded_models: [], warnings: [] },
            { unit_id: 'sync-2', asset_id: assetOne.id, target_id: 'target-b', status: 'incompatible', code: 'incompatible_target', retryable: false, effective_models: [], excluded_models: [], warnings: [] },
            { unit_id: 'sync-3', asset_id: assetTwo.id, target_id: 'target-a', status: 'needs_reconcile', code: 'needs_reconcile', retryable: true, effective_models: [], excluded_models: [], warnings: [] },
            { unit_id: 'sync-4', asset_id: assetTwo.id, target_id: 'target-b', status: 'synced', channel_id: '93', effective_models: ['claude-sonnet-4'], excluded_models: [], warnings: [] },
          ],
        })
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('checkbox', { name: '选择资产 OpenAI primary' }))
    await user.click(screen.getByRole('checkbox', { name: '选择资产 Claude reserve' }))
    await user.click(screen.getByRole('button', { name: '批量同步 2 个资产' }))

    const dialog = screen.getByRole('dialog', { name: '批量同步设置' })
    await user.clear(within(dialog).getByLabelText('模型'))
    await user.type(within(dialog).getByLabelText('模型'), 'gpt-4.1, claude-sonnet-4')
    await user.click(within(dialog).getByRole('button', { name: '开始同步' }))

    expect(await within(dialog).findByText('部分完成')).toBeInTheDocument()
    expect(within(dialog).getAllByText('已同步')).toHaveLength(2)
    expect(within(dialog).getByText('目标不兼容')).toBeInTheDocument()
    expect(within(dialog).getByText('需要校验')).toBeInTheDocument()
    expect(within(dialog).queryByLabelText('一次性安全证明')).not.toBeInTheDocument()
    expect(within(dialog).queryByText('允许兼容认证文件迁移')).not.toBeInTheDocument()
    expect(screen.queryByRole('toolbar', { name: '批量操作' })).not.toBeInTheDocument()

    const syncCalls = fetchMock.mock.calls.filter(([input]) => String(input) === '/api/v1/sync')
    expect(syncCalls).toHaveLength(1)
    const body = JSON.parse(String(syncCalls[0]?.[1]?.body)) as Record<string, unknown>
    expect(body).not.toHaveProperty('provider')
    expect(body).not.toHaveProperty('base_url')
    expect(body).not.toHaveProperty('grant')
    expect(body).toMatchObject({ units: [
      { unit_id: 'sync-1', asset_id: assetOne.id, target_id: 'target-a' },
      { unit_id: 'sync-2', asset_id: assetOne.id, target_id: 'target-b' },
      { unit_id: 'sync-3', asset_id: assetTwo.id, target_id: 'target-a' },
      { unit_id: 'sync-4', asset_id: assetTwo.id, target_id: 'target-b' },
    ] })
    expect(window.localStorage).toHaveLength(0)
    expect(window.sessionStorage).toHaveLength(0)
    expect(window.location.search).toBe('')
  })

  it('renders backend target creation failures as failures instead of unsynced cells', async () => {
    installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/sync' && init.method === 'POST') {
        return envelope({
          units: [
            { unit_id: 'sync-1', asset_id: assetOne.id, target_id: 'target-a', status: 'failed', code: 'target_create_failed', retryable: true, effective_models: [], excluded_models: [], warnings: [] },
            { unit_id: 'sync-2', asset_id: assetOne.id, target_id: 'target-b', status: 'synced', channel_id: '94', effective_models: ['gpt-4.1'], excluded_models: [], warnings: [] },
          ],
        })
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('checkbox', { name: '选择资产 OpenAI primary' }))
    await user.click(screen.getByRole('button', { name: '批量同步 1 个资产' }))
    const dialog = screen.getByRole('dialog', { name: '批量同步设置' })
    await user.click(within(dialog).getByRole('button', { name: '开始同步' }))

    expect(await within(dialog).findByText('部分完成')).toBeInTheDocument()
    expect(within(dialog).getByText('同步失败')).toBeInTheDocument()
    expect(within(dialog).queryByText('未同步')).not.toBeInTheDocument()
  })

  it('keeps the submitted target set stable while a failed request is in flight', async () => {
    let finishSync: ((response: Response) => void) | undefined
    const pendingSync = new Promise<Response>((resolve) => {
      finishSync = resolve
    })
    installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/sync' && init.method === 'POST') return pendingSync
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('checkbox', { name: '选择资产 OpenAI primary' }))
    await user.click(screen.getByRole('button', { name: '批量同步 1 个资产' }))
    const dialog = screen.getByRole('dialog', { name: '批量同步设置' })
    await user.click(within(dialog).getByRole('button', { name: '开始同步' }))
    await user.click(within(dialog).getByRole('checkbox', { name: 'Target Beta' }))
    finishSync?.(failure('upstream_failure', '同步请求失败', 502))

    expect(await within(dialog).findByRole('heading', { name: '同步失败' })).toBeInTheDocument()
    const resultList = within(dialog).getByRole('list')
    expect(within(resultList).getByText('Target Alpha')).toBeInTheDocument()
    expect(within(resultList).getByText('Target Beta')).toBeInTheDocument()
    expect(within(resultList).getAllByText('同步失败')).toHaveLength(2)
    expect(within(dialog).getAllByText('同步失败')).toHaveLength(3)
  })

  it('only submits unsynced cells and excludes needs-reconcile assets', async () => {
    const fetchMock = installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') {
        return envelope(
          matrix([
            {
              asset: assetOne,
              cells: [
                { target_id: 'target-a', status: 'needs_reconcile' },
                { target_id: 'target-b', status: 'unsynced' },
              ],
            },
            {
              asset: assetTwo,
              cells: [
                { target_id: 'target-a', status: 'needs_reconcile' },
                { target_id: 'target-b', status: 'needs_reconcile' },
              ],
            },
          ]),
        )
      }
      if (url.pathname === '/api/v1/sync' && init.method === 'POST') {
        return envelope({
          units: [{
            unit_id: 'sync-1',
            asset_id: assetOne.id,
            target_id: 'target-b',
            status: 'synced',
            channel_id: '94',
            effective_models: ['gpt-4.1'],
            excluded_models: [],
            warnings: [],
          }],
        })
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    expect(screen.getByRole('checkbox', { name: '选择资产 Claude reserve' })).toBeDisabled()
    await user.click(screen.getByRole('checkbox', { name: '选择资产 OpenAI primary' }))
    await user.click(screen.getByRole('button', { name: '批量同步 1 个资产' }))

    const dialog = screen.getByRole('dialog', { name: '批量同步设置' })
    const targetAlpha = within(dialog).getByRole('checkbox', { name: 'Target Alpha' })
    const targetBeta = within(dialog).getByRole('checkbox', { name: 'Target Beta' })
    expect(targetAlpha).toBeDisabled()
    expect(targetAlpha).not.toBeChecked()
    expect(targetBeta).toBeChecked()
    await user.click(within(dialog).getByRole('button', { name: '开始同步' }))

    expect(await within(dialog).findByText('同步完成')).toBeInTheDocument()
    const syncCall = fetchMock.mock.calls.find(([input]) => String(input) === '/api/v1/sync')
    expect(JSON.parse(String(syncCall?.[1]?.body))).toMatchObject({
      upstream_id: 'source-a',
      units: [{
        unit_id: 'sync-1',
        asset_id: assetOne.id,
        target_id: 'target-b',
        settings: { target_group: 'default' },
      }],
    })
  })

  it('shows drift details and accepts the live target state', async () => {
    const driftRows: MatrixRow[] = [
      {
        asset: assetOne,
        cells: [
          {
            target_id: 'target-a',
            status: 'drifted',
            channel_id: '42',
            differences: [{ field: 'weight', expected: '100', actual: '80' }],
          },
          { target_id: 'target-b', status: 'synced', channel_id: '77' },
        ],
      },
    ]
    const fetchMock = installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix(driftRows))
      if (url.pathname === '/api/v1/targets/target-a/drift/accept' && init.method === 'POST') {
        return envelope({ status: 'synced' })
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('link', { name: '漂移修复' }))

    expect(screen.getByRole('heading', { name: '配置漂移' })).toBeInTheDocument()
    expect(screen.getByText('权重')).toBeInTheDocument()
    expect(screen.getByText('100 -> 80')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '接受 OpenAI primary 在 Target Alpha 的目标端状态' }))

    expect(await screen.findByText('漂移已接受')).toBeInTheDocument()
    expect(screen.getByText('当前没有配置漂移')).toBeInTheDocument()
    const acceptCall = fetchMock.mock.calls.find(([input]) => String(input).includes('/drift/accept'))
    expect(JSON.parse(String(acceptCall?.[1]?.body))).toEqual({
      upstream_asset_id: assetOne.id,
      channel_id: '42',
    })
  })

  it('searches and filters managed and native channels without another API request', async () => {
    let channelReads = 0
    installFetch((url) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/targets/target-a/channels') {
        channelReads += 1
        return envelope({
          channels: [
            {
              id: '42',
              name: 'Managed OpenAI',
              provider: 'openai',
              raw_type: '1',
              base_url: 'https://provider.invalid',
              models: ['gpt-4.1'],
              group: 'default',
              priority: 0,
              weight: 100,
              enabled: true,
              managed: true,
              upstream_asset_id: assetOne.id,
            },
            {
              id: '9',
              name: 'Native channel',
              provider: 'gemini',
              raw_type: '24',
              base_url: '',
              models: ['gemini-2.5-pro'],
              group: 'default',
              priority: 1,
              weight: 80,
              enabled: true,
              managed: false,
            },
          ],
        })
      }
      throw new Error(`Unexpected request: ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await openTargetChannels(user)

    expect(await screen.findByText('Managed OpenAI')).toBeInTheDocument()
    expect(screen.getByText('SyncHub 管理')).toBeInTheDocument()
    expect(screen.getByText('原生渠道')).toBeInTheDocument()
    expect(screen.getByText(assetOne.id)).toBeInTheDocument()

    const channelSearch = screen.getByRole('searchbox', { name: '搜索渠道' })
    await user.type(channelSearch, 'native')
    expect(screen.queryByText('Managed OpenAI')).not.toBeInTheDocument()
    expect(screen.getByText('Native channel')).toBeInTheDocument()

    await user.clear(channelSearch)
    const sourceFilter = screen.getByRole('combobox', { name: '来源' })
    await user.selectOptions(sourceFilter, 'managed')
    expect(screen.getByText('Managed OpenAI')).toBeInTheDocument()
    expect(screen.queryByText('Native channel')).not.toBeInTheDocument()
    await user.selectOptions(sourceFilter, 'native')
    expect(screen.queryByText('Managed OpenAI')).not.toBeInTheDocument()
    expect(screen.getByText('Native channel')).toBeInTheDocument()
    expect(channelReads).toBe(1)
  })

  it('reloads live target channels every time the user enters the page', async () => {
    let channelReads = 0
    installFetch((url) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/targets/target-a/channels') {
        channelReads += 1
        return envelope({
          channels: [{
            id: String(channelReads),
            name: channelReads === 1 ? 'First live channel' : 'Second live channel',
            provider: 'openai',
            raw_type: '1',
            base_url: '',
            models: ['gpt-4.1'],
            group: 'default',
            priority: 0,
            weight: 100,
            enabled: true,
            managed: false,
          }],
        })
      }
      throw new Error(`Unexpected request: ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await openTargetChannels(user)
    expect(await screen.findByText('First live channel')).toBeInTheDocument()
    await user.click(screen.getByRole('link', { name: '同步工作台' }))
    await openTargetChannels(user)

    expect(await screen.findByText('Second live channel')).toBeInTheDocument()
    expect(channelReads).toBe(2)
  })

  it('removes a managed channel from the matrix immediately after deletion', async () => {
    const syncedMatrix = matrix([{ asset: assetOne, cells: [{ target_id: 'target-a', status: 'synced', channel_id: '42' }] }])
    installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope({ ...config, targets: [targetA] })
      if (url.pathname === '/api/v1/matrix') return envelope(syncedMatrix)
      if (url.pathname === '/api/v1/targets/target-a/channels' && (init.method ?? 'GET') === 'GET') {
        return envelope({ channels: [{
          id: '42',
          name: 'Managed OpenAI',
          provider: 'openai',
          raw_type: '1',
          base_url: '',
          models: ['gpt-4.1'],
          group: 'default',
          priority: 0,
          weight: 100,
          enabled: true,
          managed: true,
          upstream_asset_id: assetOne.id,
        }] })
      }
      if (url.pathname === '/api/v1/targets/target-a/channels/42' && init.method === 'DELETE') return envelope({})
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await openTargetChannels(user)
    await screen.findByText('Managed OpenAI')
    await user.click(screen.getByRole('button', { name: '删除渠道 Managed OpenAI' }))
    await user.click(within(screen.getByRole('dialog', { name: '删除目标渠道' })).getByRole('button', { name: '确认删除' }))
    await user.click(screen.getByRole('link', { name: '同步工作台' }))

    const matrixTable = document.querySelector<HTMLElement>('.matrix-table')
    expect(matrixTable).not.toBeNull()
    expect(within(matrixTable!).getByText('未同步')).toBeInTheDocument()
    expect(document.querySelector('.status-synced')).not.toBeInTheDocument()
  })

  it('keeps credential management out of settings and routes it to dedicated workspaces', async () => {
    const fetchMock = installConsoleApi()
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    const navigation = screen.getByRole('navigation', { name: '主导航' })
    await user.click(within(navigation).getByRole('link', { name: '系统设置' }))

    expect(screen.getByRole('region', { name: '运行参数' })).toBeInTheDocument()
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '添加目标实例' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '添加上游实例' })).not.toBeInTheDocument()
    expect(document.querySelector('input[type="password"]')).toBeNull()
    expect(window.localStorage).toHaveLength(0)
    expect(window.sessionStorage).toHaveLength(0)

    await user.click(within(navigation).getByRole('link', { name: '上游连接' }))
    expect(await screen.findByRole('heading', { name: '上游连接' })).toBeInTheDocument()
    expect(window.location.pathname).toBe('/upstreams')

    await user.click(within(navigation).getByRole('link', { name: '目标实例' }))
    expect(await screen.findByRole('heading', { name: '目标实例' })).toBeInTheDocument()
    expect(window.location.pathname).toBe('/targets')

    const writeCalls = fetchMock.mock.calls.filter(([, init]) =>
      ['POST', 'PUT', 'PATCH', 'DELETE'].includes(String(init?.method ?? 'GET').toUpperCase()),
    )
    expect(writeCalls).toHaveLength(0)
  })

  it('opens and closes the narrow-screen navigation after selecting a page', async () => {
    installConsoleApi()
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    const menuButton = screen.getByRole('button', { name: '打开导航' })
    expect(menuButton).toHaveAttribute('aria-expanded', 'false')
    await user.click(menuButton)

    expect(menuButton).toHaveAttribute('aria-expanded', 'true')
    const drawer = screen.getByRole('dialog', { name: '控制台导航' })
    expect(drawer).toHaveAttribute('aria-modal', 'true')
    const mobileNavigation = within(drawer).getByRole('navigation', { name: '移动端主导航' })
    expect(mobileNavigation).toHaveAttribute('data-open', 'true')
    const closeButton = within(drawer).getByRole('button', { name: '关闭导航' })
    const lastMobileItem = within(mobileNavigation).getByRole('link', { name: '系统设置' })
    expect(closeButton).toHaveFocus()
    await fireEvent.keyDown(document, { key: 'Tab', shiftKey: true })
    expect(lastMobileItem).toHaveFocus()
    await fireEvent.keyDown(document, { key: 'Tab' })
    expect(closeButton).toHaveFocus()
    await user.click(within(mobileNavigation).getByRole('link', { name: '系统设置' }))

    expect(menuButton).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument()
    expect(document.getElementById('main-content')).toHaveFocus()
  })

  it('closes the mobile navigation with Escape and restores focus to its trigger', async () => {
    installConsoleApi()
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    const menuButton = screen.getByRole('button', { name: '打开导航' })
    menuButton.focus()
    await user.click(menuButton)
    expect(screen.getByRole('navigation', { name: '移动端主导航' })).toBeInTheDocument()

    await fireEvent.keyDown(document, { key: 'Escape' })

    expect(screen.queryByRole('navigation', { name: '移动端主导航' })).not.toBeInTheDocument()
    expect(menuButton).toHaveFocus()
  })

  it('retries a live channel failure, then edits and deletes channels', async () => {
    let channelReads = 0
    const fetchMock = installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/targets/target-a/channels' && (init.method ?? 'GET') === 'GET') {
        channelReads += 1
        if (channelReads === 1) return failure('upstream_failure', '目标暂时不可用', 502)
        return envelope({
          channels: [
            {
              id: '42',
              name: 'Managed channel',
              provider: 'openai',
              raw_type: '1',
              base_url: 'https://provider.invalid',
              models: ['gpt-4.1'],
              group: 'default',
              priority: 0,
              weight: 100,
              enabled: true,
              managed: true,
              upstream_asset_id: assetOne.id,
            },
            {
              id: '9',
              name: 'Native channel',
              provider: 'gemini',
              raw_type: '24',
              base_url: '',
              models: ['gemini-2.5-pro'],
              group: 'default',
              priority: 1,
              weight: 80,
              enabled: true,
              managed: false,
            },
          ],
        })
      }
      if (url.pathname === '/api/v1/targets/target-a/channels/42' && init.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>
        return envelope({
          id: '42',
          provider: 'openai',
          raw_type: '1',
          managed: true,
          upstream_asset_id: assetOne.id,
          ...body,
        })
      }
      if (url.pathname === '/api/v1/targets/target-a/channels/9' && init.method === 'DELETE') {
        return envelope({})
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await openTargetChannels(user)
    expect(await screen.findByRole('alert')).toHaveTextContent('目标暂时不可用')
    await user.click(screen.getByRole('button', { name: '重试' }))
    await screen.findByText('Managed channel')

    await user.click(screen.getByRole('button', { name: '编辑渠道 Managed channel' }))
    const editDialog = screen.getByRole('dialog', { name: '编辑目标渠道' })
    await user.clear(within(editDialog).getByLabelText('名称'))
    await user.type(within(editDialog).getByLabelText('名称'), 'Managed channel updated')
    await user.click(within(editDialog).getByRole('button', { name: '保存渠道' }))
    expect(await screen.findByText('Managed channel updated')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '删除渠道 Native channel' }))
    const deleteDialog = screen.getByRole('dialog', { name: '删除目标渠道' })
    await user.click(within(deleteDialog).getByRole('button', { name: '确认删除' }))
    await waitFor(() => expect(screen.queryByText('Native channel')).not.toBeInTheDocument())
    expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith('/channels/9'))).toBe(true)
  })

  it('shows runtime settings directly without writing to the API', async () => {
    const fetchMock = installConsoleApi()
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(
      within(screen.getByRole('navigation', { name: '主导航' }))
        .getByRole('link', { name: '系统设置' }),
    )

    const runtimeSettings = screen.getByRole('region', { name: '运行参数' })
    expect(within(runtimeSettings).getByLabelText('监听地址')).toHaveValue('127.0.0.1')
    expect(within(runtimeSettings).getByLabelText('端口')).toHaveValue(8888)
    expect(within(runtimeSettings).getByLabelText('校验间隔')).toHaveValue('5m0s')
    expect(within(runtimeSettings).getByLabelText('请求超时')).toHaveValue('15s')
    expect(within(runtimeSettings).getByLabelText('同步并发')).toHaveValue(4)
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument()

    const writeCalls = fetchMock.mock.calls.filter(([, init]) =>
      ['POST', 'PUT', 'PATCH', 'DELETE'].includes(String(init?.method ?? 'GET').toUpperCase()),
    )
    expect(writeCalls).toHaveLength(0)
  })

  it('saves runtime settings without exposing connection controls', async () => {
    const fetchMock = installFetch((url, init) => {
      if (url.pathname === '/api/v1/health') {
        return envelope({ status: 'ok', version: 'v1.3.0', build_date: '2026-07-29T16:00:00+08:00' })
      }
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/config/app' && init.method === 'PUT') {
        return envelope(JSON.parse(String(init.body)))
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('link', { name: '系统设置' }))

    const concurrency = screen.getByLabelText('同步并发')
    await user.clear(concurrency)
    await user.type(concurrency, '6')
    await user.click(screen.getByRole('button', { name: '保存运行设置' }))
    expect(await screen.findByText('运行设置已保存')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '添加目标实例' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '添加上游实例' })).not.toBeInTheDocument()

    const writeCalls = fetchMock.mock.calls.filter(([, init]) =>
      ['POST', 'PUT', 'PATCH', 'DELETE'].includes(String(init?.method ?? 'GET').toUpperCase()),
    )
    expect(writeCalls).toHaveLength(1)
    expect(writeCalls[0]?.[0]).toBe('/api/v1/config/app')
    expect(JSON.parse(String(writeCalls[0]?.[1]?.body))).toMatchObject({
      host: '127.0.0.1',
      port: 8888,
      reconcile_interval: '5m0s',
      request_timeout: '15s',
      sync_concurrency: 6,
    })
    expect(window.localStorage).toHaveLength(0)
    expect(window.sessionStorage).toHaveLength(0)
  })

  it('validates sync settings, selects all assets, and clears modal state on Escape', async () => {
    installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') {
        return envelope(
          matrix([
            {
              asset: assetOne,
              cells: [
                { target_id: 'target-a', status: 'unsynced' },
                { target_id: 'target-b', status: 'unsynced' },
              ],
            },
            {
              asset: assetTwo,
              cells: [
                { target_id: 'target-a', status: 'unsynced' },
                { target_id: 'target-b', status: 'incompatible' },
              ],
            },
          ]),
        )
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('checkbox', { name: '选择全部可同步资产' }))
    await user.click(screen.getByRole('button', { name: '批量同步 2 个资产' }))
    const dialog = screen.getByRole('dialog', { name: '批量同步设置' })
    await user.clear(within(dialog).getByLabelText('模型'))
    await user.click(within(dialog).getByRole('button', { name: '开始同步' }))
    expect(within(dialog).getByRole('alert')).toHaveTextContent('至少填写一个模型')

    await user.type(within(dialog).getByLabelText('模型'), 'model-a')
    await user.click(within(dialog).getByRole('checkbox', { name: 'Target Alpha' }))
    await user.click(within(dialog).getByRole('checkbox', { name: 'Target Beta' }))
    await user.click(within(dialog).getByRole('button', { name: '开始同步' }))
    expect(within(dialog).getByRole('alert')).toHaveTextContent('至少选择一个目标实例')

    await fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('dialog', { name: '批量同步设置' })).not.toBeInTheDocument()
  })

  it('explains discovery-only and disabled multi-key assets without merging child rows', async () => {
    const discoveryAsset: UpstreamAsset = {
      ...assetOne,
      id: 'source-a:channel:99:key:0',
      name: 'Future provider key',
      provider: 'unknown',
      raw_type: '999 / FutureProvider',
      secret_readable: false,
    }
    const disabledChild: UpstreamAsset = {
      ...assetOne,
      id: 'source-a:channel:7:key:1',
      name: 'OpenAI primary key 2',
      enabled: false,
    }
    installConsoleApi(matrix([
      {
        asset: assetOne,
        cells: [
          { target_id: 'target-a', status: 'unsynced' },
          { target_id: 'target-b', status: 'unsynced' },
        ],
      },
      {
        asset: disabledChild,
        cells: [
          { target_id: 'target-a', status: 'unsynced' },
          { target_id: 'target-b', status: 'unsynced' },
        ],
      },
      {
        asset: discoveryAsset,
        cells: [
          { target_id: 'target-a', status: 'incompatible' },
          { target_id: 'target-b', status: 'incompatible' },
        ],
      },
    ]))
    renderApp()

    expect(await screen.findByText('Future provider key')).toBeInTheDocument()
    expect(screen.getByText('999 / FutureProvider')).toBeInTheDocument()
    expect(screen.getByText('仅发现')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: '选择资产 Future provider key' })).toHaveAccessibleDescription(
      '仅发现：秘密不可读取',
    )
    expect(screen.getByRole('checkbox', { name: '选择资产 OpenAI primary key 2' })).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: '选择资产 OpenAI primary key 2' })).toHaveAccessibleDescription('已禁用')
    expect(screen.getByRole('checkbox', { name: '选择资产 OpenAI primary' })).toBeEnabled()
    expect(screen.getByText('source-a:channel:7:key:0')).toBeInTheDocument()
    expect(screen.getByText('source-a:channel:7:key:1')).toBeInTheDocument()
  })

  it('shows target result details and retries only failed targets without an obsolete source grant', async () => {
    let syncAttempt = 0
    const fetchMock = installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/sync' && init.method === 'POST') {
        syncAttempt += 1
        return envelope({
          units: syncAttempt === 1
            ? [
                { unit_id: 'sync-1', asset_id: assetOne.id, target_id: 'target-a', status: 'synced', channel_id: '42', effective_models: ['gpt-4.1'], excluded_models: [], warnings: [] },
                { unit_id: 'sync-2', asset_id: assetOne.id, target_id: 'target-b', status: 'failed', code: 'target_create_failed', retryable: true, effective_models: [], excluded_models: [], warnings: [] },
              ]
            : [{ unit_id: 'sync-1', asset_id: assetOne.id, target_id: 'target-b', status: 'synced', channel_id: '99', effective_models: ['gpt-4.1'], excluded_models: [], warnings: [] }],
        })
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('checkbox', { name: '选择资产 OpenAI primary' }))
    await user.click(screen.getByRole('button', { name: '批量同步 1 个资产' }))
    const dialog = screen.getByRole('dialog', { name: '批量同步设置' })
    await user.click(within(dialog).getByRole('button', { name: '开始同步' }))

    expect(await within(dialog).findByText('#42')).toBeInTheDocument()
    expect(within(dialog).getByText('target_create_failed')).toBeInTheDocument()
    expect(within(dialog).getByText('可重试')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: '重试失败目标' }))

    expect(await within(dialog).findByText('#99')).toBeInTheDocument()
    expect(syncAttempt).toBe(2)
    const syncCalls = fetchMock.mock.calls.filter(([input]) => String(input) === '/api/v1/sync')
    const retryBody = JSON.parse(String(syncCalls[1]?.[1]?.body)) as Record<string, unknown>
    expect(retryBody).toMatchObject({
      units: [{ asset_id: assetOne.id, target_id: 'target-b' }],
    })
    expect(retryBody).not.toHaveProperty('grant')
    expect(window.localStorage).toHaveLength(0)
    expect(window.sessionStorage).toHaveLength(0)
  })

  it('reports partial reconcile failures without discarding drift details', async () => {
    const driftMatrix = matrix([
      {
        asset: assetOne,
        cells: [
          {
            target_id: 'target-a',
            status: 'drifted',
            channel_id: '42',
            differences: [{ field: 'models', expected: ['gpt-4.1'], actual: ['gpt-4.1-mini'] }],
          },
          { target_id: 'target-b', status: 'synced', channel_id: '77' },
        ],
      },
    ])
    let matrixReads = 0
    installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') {
        matrixReads += 1
        return envelope(driftMatrix)
      }
      if (url.pathname === '/api/v1/targets/target-a/reconcile' && init.method === 'POST') return envelope({})
      if (url.pathname === '/api/v1/targets/target-b/reconcile' && init.method === 'POST') {
        return failure('upstream_timeout', '目标校验超时', 504)
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('link', { name: '漂移修复' }))
    await user.click(screen.getByRole('button', { name: '校验全部目标' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('部分目标校验失败')
    expect(screen.getByText('模型')).toBeInTheDocument()
    expect(matrixReads).toBe(2)
  })
})
