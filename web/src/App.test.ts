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
}

const targetB = {
  id: 'target-b',
  name: 'Target Beta',
  type: 'cliproxyapi',
  base_url: 'https://target-b.invalid',
}

const upstream = {
  id: 'source-a',
  name: 'Source Alpha',
  type: 'newapi',
  base_url: 'https://source.invalid',
  sync_mappings: [],
}

const genericUpstream = {
  id: 'source-generic',
  name: 'Generic Source',
  type: 'generic',
  base_url: 'https://generic-source.invalid',
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
    expect(within(topbar).getByText('SyncHub')).toBeInTheDocument()
    expect(within(topbar).getByLabelText('本地管理 API')).toHaveTextContent('最近检查正常')
    expect(within(topbar).getByText('同步工作台')).toBeInTheDocument()

    const navigation = screen.getByRole('navigation', { name: '主导航' })
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

    expect(await screen.findByText('版本 v1.3.0')).toBeInTheDocument()
    expect(screen.getByText('编译 2026-07-29 16:00:00')).toBeInTheDocument()
    expect(within(screen.getByLabelText('本地管理 API')).getByText('最近检查正常')).toBeInTheDocument()
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
    expect(screen.getByRole('button', { name: '前往设置' })).toBeEnabled()
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

  it('clears a target credential after a failed submit and never persists it', async () => {
    const fetchMock = installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/targets' && init.method === 'POST') {
        return failure('invalid_request', '目标配置无效')
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const logSpy = vi.spyOn(console, 'log')
    const errorSpy = vi.spyOn(console, 'error')
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('link', { name: '系统设置' }))
    await user.click(screen.getByRole('button', { name: '添加目标实例' }))

    const dialog = screen.getByRole('dialog', { name: '添加目标实例' })
    await user.type(within(dialog).getByLabelText('实例 ID'), 'target-new')
    await user.type(within(dialog).getByLabelText('名称'), 'New target')
    await user.type(within(dialog).getByLabelText('Base URL'), 'https://new-target.invalid')
    const credential = within(dialog).getByLabelText('访问令牌')
    await user.type(credential, 'temporary-form-value')
    await user.click(within(dialog).getByRole('button', { name: '保存目标实例' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('目标配置无效')
    expect(credential).toHaveValue('')
    expect(window.localStorage).toHaveLength(0)
    expect(window.sessionStorage).toHaveLength(0)
    expect(window.location.href).not.toContain('temporary-form-value')
    expect(logSpy).not.toHaveBeenCalled()
    expect(errorSpy).not.toHaveBeenCalled()
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/targets',
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('validates instance URLs before sending credentials', async () => {
    const fetchMock = installConsoleApi()
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('link', { name: '系统设置' }))
    await user.click(screen.getByRole('button', { name: '添加上游实例' }))

    const dialog = screen.getByRole('dialog', { name: '添加上游实例' })
    await user.type(within(dialog).getByLabelText('实例 ID'), 'source-new')
    await user.type(within(dialog).getByLabelText('名称'), 'New source')
    await user.type(within(dialog).getByLabelText('Base URL'), 'not-a-url')
    await user.type(within(dialog).getByLabelText('访问令牌'), 'temporary-form-value')
    await user.click(within(dialog).getByRole('button', { name: '保存上游实例' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('请输入绝对 HTTP(S) 地址')
    expect(fetchMock.mock.calls.some(([input]) => String(input) === '/api/v1/upstreams')).toBe(false)
  })

  it('creates a New API target with a positive user ID and shows the sanitized identity', async () => {
    const fetchMock = installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/targets' && init.method === 'POST') {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>
        return envelope({
          id: body.id,
          name: body.name,
          type: body.type,
          base_url: body.base_url,
          user_id: body.user_id,
        })
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('link', { name: '系统设置' }))
    await user.click(screen.getByRole('button', { name: '添加目标实例' }))
    const dialog = screen.getByRole('dialog', { name: '添加目标实例' })
    await user.type(within(dialog).getByLabelText('实例 ID'), 'target-with-user')
    await user.type(within(dialog).getByLabelText('名称'), 'Identity target')
    await user.type(within(dialog).getByLabelText('Base URL'), 'https://identity-target.invalid')
    await user.type(within(dialog).getByLabelText('New API 用户 ID'), '73')
    await user.type(within(dialog).getByLabelText('访问令牌'), 'E2E_TARGET_TOKEN_PLACEHOLDER')
    await user.click(within(dialog).getByRole('button', { name: '保存目标实例' }))

    expect(await screen.findByText('Identity target')).toBeInTheDocument()
    expect(screen.getByText('用户 ID 73')).toBeInTheDocument()
    const createCall = fetchMock.mock.calls.find(([input]) => String(input) === '/api/v1/targets')
    expect(JSON.parse(String(createCall?.[1]?.body))).toMatchObject({
      type: 'newapi',
      user_id: 73,
      access_token: 'E2E_TARGET_TOKEN_PLACEHOLDER',
    })
    expect(JSON.parse(String(createCall?.[1]?.body))).not.toHaveProperty('proxy_api_key')
    expect(document.body).not.toHaveTextContent('E2E_TARGET_TOKEN_PLACEHOLDER')
    expect(window.localStorage).toHaveLength(0)
    expect(window.sessionStorage).toHaveLength(0)
  })

  it('refills and explicitly clears a configured New API user ID while editing', async () => {
    const configuredTarget = { ...targetA, user_id: 41 }
    let updateBody: Record<string, unknown> | undefined
    installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') {
        return envelope({ ...config, targets: [configuredTarget, targetB] })
      }
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/targets/target-a' && init.method === 'PUT') {
        updateBody = JSON.parse(String(init.body)) as Record<string, unknown>
        return envelope({ ...targetA, name: updateBody.name, base_url: updateBody.base_url })
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('link', { name: '系统设置' }))
    expect(screen.getByText('用户 ID 41')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '编辑目标实例 Target Alpha' }))
    const dialog = screen.getByRole('dialog', { name: '编辑目标实例' })
    const userIdInput = within(dialog).getByLabelText('New API 用户 ID')
    expect(userIdInput).toHaveValue(41)
    await user.clear(userIdInput)
    await user.click(within(dialog).getByRole('button', { name: '保存目标实例' }))

    await waitFor(() => expect(updateBody).toMatchObject({ user_id: 0 }))
    expect(screen.queryByText('用户 ID 41')).not.toBeInTheDocument()
  })

  it('rejects invalid New API user IDs and omits the field after a platform switch', async () => {
    const fetchMock = installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/upstreams' && init.method === 'POST') {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>
        return envelope({
          id: body.id,
          name: body.name,
          type: body.type,
          base_url: body.base_url,
          sync_mappings: [],
        })
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('link', { name: '系统设置' }))
    await user.click(screen.getByRole('button', { name: '添加上游实例' }))
    const dialog = screen.getByRole('dialog', { name: '添加上游实例' })
    await user.type(within(dialog).getByLabelText('实例 ID'), 'source-switched')
    await user.type(within(dialog).getByLabelText('名称'), 'Switched source')
    await user.type(within(dialog).getByLabelText('Base URL'), 'https://switched-source.invalid')
    await user.type(within(dialog).getByLabelText('访问令牌'), 'E2E_MANAGEMENT_KEY_PLACEHOLDER')
    const userIdInput = within(dialog).getByLabelText('New API 用户 ID')
    await user.type(userIdInput, '1.5')
    await fireEvent.submit(dialog.querySelector('form')!)
    expect(within(dialog).getByRole('alert')).toHaveTextContent('New API 用户 ID 必须为正整数')
    expect(fetchMock.mock.calls.some(([input]) => String(input) === '/api/v1/upstreams')).toBe(false)

    await user.clear(userIdInput)
    await user.type(userIdInput, '0')
    await fireEvent.submit(dialog.querySelector('form')!)
    expect(within(dialog).getByRole('alert')).toHaveTextContent('New API 用户 ID 必须为正整数')

    await user.clear(userIdInput)
    await user.type(userIdInput, '-2')
    await fireEvent.submit(dialog.querySelector('form')!)
    expect(within(dialog).getByRole('alert')).toHaveTextContent('New API 用户 ID 必须为正整数')

    await user.clear(userIdInput)
    await user.type(userIdInput, '17')
    await user.selectOptions(within(dialog).getByLabelText('平台类型'), 'generic')
    expect(within(dialog).queryByLabelText('New API 用户 ID')).not.toBeInTheDocument()
    expect(within(dialog).getByLabelText('API Key')).toHaveValue('E2E_MANAGEMENT_KEY_PLACEHOLDER')
    await user.click(within(dialog).getByRole('button', { name: '保存上游实例' }))

    expect(await screen.findByText('Switched source')).toBeInTheDocument()
    const createCall = fetchMock.mock.calls.find(([input]) => String(input) === '/api/v1/upstreams')
    const createBody = JSON.parse(String(createCall?.[1]?.body))
    expect(createBody).toMatchObject({ type: 'generic', api_key: 'E2E_MANAGEMENT_KEY_PLACEHOLDER' })
    expect(createBody).not.toHaveProperty('user_id')
    expect(createBody).not.toHaveProperty('access_token')
    expect(createBody).not.toHaveProperty('management_key')
  })

  it('creates and edits a generic upstream with only its URL and shared API key', async () => {
    let updateBody: Record<string, unknown> | undefined
    const fetchMock = installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/upstreams' && init.method === 'POST') {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>
        return envelope({
          id: body.id,
          name: body.name,
          type: body.type,
          base_url: body.base_url,
          sync_mappings: [],
        })
      }
      if (url.pathname === '/api/v1/upstreams/source-generic' && init.method === 'PUT') {
        updateBody = JSON.parse(String(init.body)) as Record<string, unknown>
        return envelope({ ...genericUpstream, ...updateBody })
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('link', { name: '系统设置' }))
    await user.click(screen.getByRole('button', { name: '添加上游实例' }))
    const dialog = screen.getByRole('dialog', { name: '添加上游实例' })
    expect(within(dialog).getByRole('option', { name: 'New API' })).toBeInTheDocument()
    expect(within(dialog).getByRole('option', { name: '通用 API' })).toBeInTheDocument()
    expect(within(dialog).queryByRole('option', { name: 'CLIProxyAPI' })).not.toBeInTheDocument()
    expect(within(dialog).queryByRole('option', { name: 'Sub2Api' })).not.toBeInTheDocument()
    await user.selectOptions(within(dialog).getByLabelText('平台类型'), 'generic')
    await user.type(within(dialog).getByLabelText('实例 ID'), 'source-generic')
    await user.type(within(dialog).getByLabelText('名称'), 'Generic Source')
    await user.type(within(dialog).getByLabelText('Base URL'), 'https://generic-source.invalid/')
    const apiKey = within(dialog).getByLabelText('API Key')
    expect(apiKey).toHaveAttribute('type', 'password')
    expect(apiKey).toHaveAttribute('autocomplete', 'off')
    expect(within(dialog).queryByLabelText('New API 用户 ID')).not.toBeInTheDocument()
    expect(within(dialog).queryByLabelText('管理密钥')).not.toBeInTheDocument()
    await user.type(apiKey, 'GENERIC_CREATE_KEY_PLACEHOLDER')
    await user.click(within(dialog).getByRole('button', { name: '保存上游实例' }))

    expect(await screen.findByText('Generic Source')).toBeInTheDocument()
    const createCall = fetchMock.mock.calls.find(([input]) => String(input) === '/api/v1/upstreams')
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      id: 'source-generic',
      name: 'Generic Source',
      type: 'generic',
      base_url: 'https://generic-source.invalid',
      api_key: 'GENERIC_CREATE_KEY_PLACEHOLDER',
    })
    expect(document.body).not.toHaveTextContent('GENERIC_CREATE_KEY_PLACEHOLDER')
    expect(window.localStorage).toHaveLength(0)
    expect(window.sessionStorage).toHaveLength(0)

    await user.click(screen.getByRole('button', { name: '编辑上游实例 Generic Source' }))
    const editDialog = screen.getByRole('dialog', { name: '编辑上游实例' })
    expect(within(editDialog).getByLabelText('API Key')).toHaveValue('')
    await user.type(within(editDialog).getByLabelText('API Key'), 'GENERIC_REPLACE_KEY_PLACEHOLDER')
    await user.click(within(editDialog).getByRole('button', { name: '保存上游实例' }))

    await waitFor(() => expect(updateBody).toBeDefined())
    expect(updateBody).toEqual({
      name: 'Generic Source',
      base_url: 'https://generic-source.invalid',
      api_key: 'GENERIC_REPLACE_KEY_PLACEHOLDER',
    })
    expect(document.body).not.toHaveTextContent('GENERIC_REPLACE_KEY_PLACEHOLDER')
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
    const mobileNavigation = screen.getByRole('navigation', { name: '移动端主导航' })
    expect(mobileNavigation).toHaveAttribute('data-open', 'true')
    const activeMobileItem = within(mobileNavigation).getByRole('link', { name: '同步工作台' })
    const lastMobileItem = within(mobileNavigation).getByRole('link', { name: '系统设置' })
    expect(activeMobileItem).toHaveFocus()
    await fireEvent.keyDown(document, { key: 'Tab', shiftKey: true })
    expect(lastMobileItem).toHaveFocus()
    await fireEvent.keyDown(document, { key: 'Tab' })
    expect(activeMobileItem).toHaveFocus()
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

  it('switches settings tabs without writing to the API', async () => {
    const fetchMock = installFetch((url) => {
      if (url.pathname === '/api/v1/health') {
        return envelope({ status: 'ok', version: 'v1.3.0', build_date: '2026-07-29T16:00:00+08:00' })
      }
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      throw new Error(`Unexpected request: ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(within(screen.getByRole('navigation', { name: '主导航' })).getByRole('link', { name: '系统设置' }))

    const tablist = screen.getByRole('tablist', { name: '设置分类' })
    const instanceTab = within(tablist).getByRole('tab', { name: '实例管理' })
    const runtimeTab = within(tablist).getByRole('tab', { name: '运行参数' })
    expect(instanceTab).toHaveAttribute('aria-selected', 'true')
    expect(instanceTab).toHaveAttribute('tabindex', '0')
    expect(runtimeTab).toHaveAttribute('aria-selected', 'false')
    expect(runtimeTab).toHaveAttribute('tabindex', '-1')
    const instancePanel = screen.getByRole('tabpanel', { name: '实例管理' })
    expect(within(instancePanel).getByRole('heading', { name: '目标实例' })).toBeInTheDocument()
    expect(within(instancePanel).getByRole('heading', { name: '上游实例' })).toBeInTheDocument()
    expect(document.getElementById('settings-runtime-panel')).toHaveAttribute('hidden')

    instanceTab.focus()
    await fireEvent.keyDown(instanceTab, { key: 'ArrowRight' })
    await waitFor(() => expect(runtimeTab).toHaveFocus())
    expect(instanceTab).toHaveAttribute('aria-selected', 'false')
    expect(runtimeTab).toHaveAttribute('aria-selected', 'true')
    const runtimePanel = screen.getByRole('tabpanel', { name: '运行参数' })
    expect(within(runtimePanel).getByLabelText('同步并发')).toHaveValue(4)
    expect(screen.queryByRole('button', { name: '添加目标实例' })).not.toBeInTheDocument()

    await fireEvent.keyDown(runtimeTab, { key: 'Home' })
    await waitFor(() => expect(instanceTab).toHaveFocus())
    expect(instanceTab).toHaveAttribute('aria-selected', 'true')
    await user.click(runtimeTab)

    const writeCalls = fetchMock.mock.calls.filter(([, init]) =>
      ['POST', 'PUT', 'PATCH', 'DELETE'].includes(String(init?.method ?? 'GET').toUpperCase()),
    )
    expect(writeCalls).toHaveLength(0)
  })

  it('adds, edits, and deletes instances and saves runtime settings', async () => {
    const fetchMock = installFetch((url, init) => {
      if (url.pathname === '/api/v1/config') return envelope(config)
      if (url.pathname === '/api/v1/matrix') return envelope(matrix())
      if (url.pathname === '/api/v1/upstreams' && init.method === 'POST') {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>
        return envelope({
          id: body.id,
          name: body.name,
          type: body.type,
          base_url: body.base_url,
          sync_mappings: [],
        })
      }
      if (url.pathname === '/api/v1/targets/target-a' && init.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>
        return envelope({ ...targetA, ...body })
      }
      if (url.pathname === '/api/v1/upstreams/source-a' && init.method === 'DELETE') return envelope({})
      if (url.pathname === '/api/v1/config/app' && init.method === 'PUT') {
        return envelope(JSON.parse(String(init.body)))
      }
      throw new Error(`Unexpected request: ${init.method ?? 'GET'} ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderApp()

    await screen.findByText('OpenAI primary')
    await user.click(screen.getByRole('link', { name: '系统设置' }))
    await user.click(screen.getByRole('button', { name: '添加上游实例' }))
    const addDialog = screen.getByRole('dialog', { name: '添加上游实例' })
    await user.type(within(addDialog).getByLabelText('实例 ID'), 'source-generic-crud')
    await user.type(within(addDialog).getByLabelText('名称'), 'Source Gamma')
    await user.selectOptions(within(addDialog).getByLabelText('平台类型'), 'generic')
    await user.type(within(addDialog).getByLabelText('Base URL'), 'https://source-generic.invalid/')
    await user.type(within(addDialog).getByLabelText('API Key'), 'temporary-form-value')
    await user.click(within(addDialog).getByRole('button', { name: '保存上游实例' }))
    expect(await screen.findByText('Source Gamma')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '编辑目标实例 Target Alpha' }))
    const editDialog = screen.getByRole('dialog', { name: '编辑目标实例' })
    await user.clear(within(editDialog).getByLabelText('名称'))
    await user.type(within(editDialog).getByLabelText('名称'), 'Target Alpha updated')
    await user.click(within(editDialog).getByRole('button', { name: '保存目标实例' }))
    expect(await screen.findByText('Target Alpha updated')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '删除上游实例 Source Alpha' }))
    const deleteDialog = screen.getByRole('dialog', { name: '删除实例' })
    await user.click(within(deleteDialog).getByRole('button', { name: '确认删除' }))
    await waitFor(() => expect(screen.queryByText('Source Alpha')).not.toBeInTheDocument())

    await user.click(screen.getByRole('tab', { name: '运行参数' }))
    const concurrency = screen.getByLabelText('同步并发')
    await user.clear(concurrency)
    await user.type(concurrency, '6')
    await user.click(screen.getByRole('button', { name: '保存运行设置' }))
    expect(await screen.findByText('运行设置已保存')).toBeInTheDocument()

    const createCall = fetchMock.mock.calls.find(([input]) => String(input) === '/api/v1/upstreams')
    expect(JSON.parse(String(createCall?.[1]?.body))).toMatchObject({
      id: 'source-generic-crud',
      type: 'generic',
      base_url: 'https://source-generic.invalid',
      api_key: 'temporary-form-value',
    })
    expect(window.localStorage).toHaveLength(0)
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
