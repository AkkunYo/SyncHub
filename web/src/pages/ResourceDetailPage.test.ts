import { createPinia } from 'pinia'
import { cleanup, render, screen, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { useConsoleStore } from '@/stores/console'
import type { SanitizedConfig } from '@/types'

import ResourceDetailPage from './ResourceDetailPage.vue'

const genericSource = {
  id: 'source-generic',
  name: '通用生产源',
  type: 'generic',
  base_url: 'https://relay.example.com/v1',
  keys: [
    {
      id: 'primary',
      name: '主 Key',
      enabled: true,
      models: ['gpt-4.1', 'gpt-4o-mini'],
      credential_present: true,
    },
  ],
  sync_mappings: [],
}

const newAPISource = {
  id: 'source-user',
  name: 'New API 用户源',
  type: 'newapi',
  base_url: 'https://source.example.com',
  user_id: 17,
  keys: [],
  sync_mappings: [],
}

const target = {
  id: 'target-main',
  name: '生产 New API',
  type: 'newapi',
  base_url: 'https://target.example.com',
  user_id: 9,
}

const config = {
  app: {
    host: '127.0.0.1',
    port: 8888,
    reconcile_interval: '5m0s',
    request_timeout: '15s',
    sync_concurrency: 4,
  },
  targets: [target],
  upstreams: [genericSource, newAPISource],
} as SanitizedConfig

function envelope(data: unknown, status = 200): Response {
  return new Response(JSON.stringify({ success: true, data, request_id: 'req-detail' }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function failure(code: string, message: string, status = 502): Response {
  return new Response(
    JSON.stringify({ success: false, error: { code, message }, request_id: 'req-detail-error' }),
    { status, headers: { 'Content-Type': 'application/json' } },
  )
}

async function renderDetail(kind: 'upstream' | 'target', upstreamId = 'source-generic') {
  const pinia = createPinia()
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/upstreams/:id',
        name: 'upstream-detail',
        component: ResourceDetailPage,
        props: { kind: 'upstream', title: '上游详情', backTo: '/upstreams', backLabel: '返回上游连接' },
      },
      {
        path: '/targets/:id',
        name: 'target-detail',
        component: ResourceDetailPage,
        props: { kind: 'target', title: '目标概览', backTo: '/targets', backLabel: '返回目标实例' },
      },
      { path: '/upstreams', name: 'upstreams', component: { template: '<div />' } },
      { path: '/targets', name: 'targets', component: { template: '<div />' } },
      { path: '/targets/:id/channels', name: 'target-channels', component: { template: '<div />' } },
      { path: '/sync', name: 'sync', component: { template: '<div />' } },
    ],
  })
  const store = useConsoleStore(pinia)
  store.config = structuredClone(config)
  store.initialState = 'ready'
  await router.push(kind === 'upstream' ? `/upstreams/${upstreamId}` : '/targets/target-main')
  await router.isReady()
  return { ...render(ResourceDetailPage, {
    props: kind === 'upstream'
      ? { kind, title: '上游详情', backTo: '/upstreams', backLabel: '返回上游连接' }
      : { kind, title: '目标概览', backTo: '/targets', backLabel: '返回目标实例' },
    global: { plugins: [pinia, router] },
  }), store }
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('Connection resource details', () => {
  it('keeps generic keys separate, exposes the sync entry, and never renders credentials', async () => {
    const fetchMock = vi.fn().mockResolvedValue(envelope({ keys: genericSource.keys }))
    vi.stubGlobal('fetch', fetchMock)
    await renderDetail('upstream')

    expect(screen.getByRole('tablist', { name: '上游详情分区' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Key 与模型' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: '连接设置' })).toHaveAttribute('aria-selected', 'false')
    const keyRow = screen.getByRole('row', { name: /主 Key/ })
    expect(within(keyRow).getByText('2 个模型')).toBeInTheDocument()
    expect(within(keyRow).getByText('凭证已配置')).toBeInTheDocument()
    expect(within(keyRow).getByText('未验证')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '进入同步工作台' })).toHaveAttribute('href', '/sync')
    expect(document.body.textContent).not.toContain('sk-')
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/upstreams/source-generic/keys',
      expect.objectContaining({ method: 'GET' }),
    )
  })

  it('adds a write-only key from the generic connection detail drawer', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      expect(String(input)).toBe('/api/v1/upstreams/source-generic/keys')
      if ((init.method ?? 'GET') === 'GET') return envelope({ keys: genericSource.keys })
      expect(init.method).toBe('POST')
      expect(JSON.parse(String(init.body))).toEqual({
        id: 'backup',
        name: '备用 Key',
        api_key: 'sk-backup',
        enabled: true,
        models: [],
      })
      return envelope({
        id: 'backup',
        name: '备用 Key',
        enabled: true,
        models: [],
        credential_present: true,
      }, 201)
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    await renderDetail('upstream')

    await user.click(screen.getByRole('button', { name: '添加 Key' }))
    const drawer = screen.getByRole('dialog', { name: '添加通用 Key' })
    await user.type(within(drawer).getByLabelText('Key ID'), 'backup')
    await user.type(within(drawer).getByLabelText('Key 别名'), '备用 Key')
    await user.type(within(drawer).getByLabelText('API Key'), 'sk-backup')
    await user.click(within(drawer).getByRole('button', { name: '保存 Key' }))

    expect(await screen.findByText('备用 Key')).toBeInTheDocument()
    expect(screen.queryByDisplayValue('sk-backup')).not.toBeInTheDocument()
  })

  it('uses target overview, channel, and settings tabs with explicit validation and channel entry', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      expect(String(input)).toBe('/api/v1/targets/target-main/connection-tests')
      expect(init.method).toBe('POST')
      return envelope({
        reachable: true,
        authenticated: true,
        authorized: true,
        resource_count: 6,
        capabilities: { supports_static_key: true, supports_proxy_endpoint: true },
      })
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    await renderDetail('target')

    const tabs = screen.getByRole('tablist', { name: '目标详情分区' })
    expect(within(tabs).getByRole('tab', { name: '概览' })).toHaveAttribute('aria-selected', 'true')
    expect(within(tabs).getByRole('tab', { name: '渠道' })).toBeInTheDocument()
    expect(within(tabs).getByRole('tab', { name: '设置' })).toBeInTheDocument()
    expect(screen.getByText('连接尚未验证')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '验证目标连接' }))
    expect(await screen.findByText('连接验证通过')).toBeInTheDocument()
    expect(screen.getByText('6 个渠道')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '查看渠道' })).toHaveAttribute(
      'href',
      '/targets/target-main/channels',
    )
  })

  it('uses the keys endpoint as the authoritative detail source with loading and retry states', async () => {
    let releaseKeys: ((response: Response) => void) | undefined
    const pendingKeys = new Promise<Response>((resolve) => {
      releaseKeys = resolve
    })
    const authoritativeKey = {
      id: 'server-key',
      name: '服务端权威 Key',
      enabled: true,
      models: ['gpt-5.2'],
      credential_present: true,
    }
    const fetchMock = vi.fn().mockReturnValue(pendingKeys)
    vi.stubGlobal('fetch', fetchMock)

    await renderDetail('upstream')

    expect(screen.getByRole('status', { name: '正在加载 Key 列表' })).toBeInTheDocument()
    releaseKeys?.(envelope({ keys: [authoritativeKey] }))
    expect(await screen.findByText('服务端权威 Key')).toBeInTheDocument()
    expect(screen.queryByText('主 Key')).not.toBeInTheDocument()

    fetchMock.mockResolvedValueOnce(failure('upstream_failure', 'Key 列表暂时不可用'))
    fetchMock.mockResolvedValueOnce(envelope({ keys: [authoritativeKey] }))
    await userEvent.setup().click(screen.getByRole('button', { name: '重新加载 Key 列表' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Key 列表暂时不可用')
    await userEvent.setup().click(screen.getByRole('button', { name: '重试 Key 列表' }))
    expect(await screen.findByText('服务端权威 Key')).toBeInTheDocument()
  })

  it('renders the backend Key summary contract without requiring a models array', async () => {
    const fetchMock = vi.fn().mockResolvedValue(envelope({
      keys: [{
        id: 'server-key',
        name: '真实摘要 Key',
        enabled: true,
        source: 'manual',
        credential_present: true,
        model_count: 3,
        discovery_status: 'succeeded',
        snapshot_status: 'ready',
        discovered_at: '2026-08-06T11:50:00Z',
      }],
    }))
    vi.stubGlobal('fetch', fetchMock)

    await renderDetail('upstream')

    const row = await screen.findByRole('row', { name: /真实摘要 Key/ })
    expect(within(row).getByText('3 个模型')).toBeInTheDocument()
    expect(within(row).getByText('已发现')).toBeInTheDocument()
    expect(within(row).getByText('凭证已配置')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('does not present a failed initial Key request as an authoritative empty list', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      failure('upstream_failure', 'Key 列表暂时不可用'),
    )
    vi.stubGlobal('fetch', fetchMock)

    await renderDetail('upstream')

    expect(await screen.findByRole('alert')).toHaveTextContent('Key 列表暂时不可用')
    expect(screen.getByText('加载失败')).toBeInTheDocument()
    expect(screen.queryByText('0 个 Key')).not.toBeInTheDocument()
    expect(screen.queryByText('尚未配置通用 Key')).not.toBeInTheDocument()
    expect(screen.queryByText('暂无已发现的用户 Key')).not.toBeInTheDocument()
  })

  it('discovers models and probes one model at a time inside the current-key modal', async () => {
    const healthyProbe = {
      key_id: 'primary',
      model: 'gpt-4o-mini',
      protocol: 'responses',
      status: 'healthy',
      latency_ms: 842,
      checked_at: '2026-08-05T06:00:00Z',
      error_code: '',
      retryable: false,
      template_version: 'v1',
    }
    const modelsResponse = {
      upstream_id: 'source-generic',
      key_id: 'primary',
      snapshot_status: 'ready',
      snapshot_scope: 'runtime',
      discovered_at: '2026-08-05T05:58:00Z',
      models: [
        { id: 'gpt-4o-mini', discovery_status: 'discovered', probe: null },
        {
          id: 'gpt-4.1',
          discovery_status: 'discovered',
          probe: {
            key_id: 'primary',
            model: 'gpt-4.1',
            protocol: 'chat_completions',
            status: 'rate_limited',
            latency_ms: 311,
            checked_at: '2026-08-05T05:30:00Z',
            error_code: 'rate_limited',
            retryable: true,
            template_version: 'v1',
          },
        },
      ],
    }
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const path = String(input)
      const method = init.method ?? 'GET'
      if (path === '/api/v1/upstreams/source-generic/keys' && method === 'GET') {
        return envelope({ keys: genericSource.keys })
      }
      if (path === '/api/v1/upstreams/source-generic/keys/primary/models' && method === 'GET') {
        return envelope(modelsResponse)
      }
      if (path === '/api/v1/upstreams/source-generic/model-discoveries' && method === 'POST') {
        expect(JSON.parse(String(init.body))).toEqual({ key_ids: ['primary'] })
        return envelope({
          task_id: 'task-discovery-1',
          key_ids: ['primary'],
          completed: true,
          status: 'partially_failed',
          items: [{
            key_id: 'primary',
            status: 'failed',
            model_count: 2,
            error_code: 'rate_limited',
            retryable: true,
          }],
        }, 202)
      }
      expect(path).toBe('/api/v1/upstreams/source-generic/keys/primary/model-probes')
      expect(method).toBe('POST')
      expect(JSON.parse(String(init.body))).toEqual({ model: 'gpt-4o-mini', protocol: 'responses' })
      return envelope(healthyProbe)
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    await renderDetail('upstream')

    const keyRow = await screen.findByRole('row', { name: /主 Key/ })
    expect(within(keyRow).getByRole('button', { name: '刷新 主 Key 模型' })).toBeInTheDocument()
    await user.click(within(keyRow).getByRole('button', { name: '查看 主 Key 模型' }))

    const modal = await screen.findByRole('dialog', { name: '主 Key 模型' })
    expect(modal).toHaveAttribute('data-presentation', 'modal')
    expect(modal).not.toHaveAttribute('data-side')
    expect(within(modal).getByText('本次请求可能产生真实费用')).toBeInTheDocument()
    expect(within(modal).getByText('输入约 20-50 Token / 输出最多 64 Token')).toBeInTheDocument()
    expect(within(modal).getByText('本次运行')).toBeInTheDocument()
    expect(within(modal).getByText('gpt-4o-mini')).toBeInTheDocument()
    expect(within(modal).getByText('gpt-4.1')).toBeInTheDocument()

    const search = within(modal).getByRole('searchbox', { name: '搜索当前 Key 的模型' })
    await user.type(search, '4o-mini')
    expect(within(modal).queryByText('gpt-4.1')).not.toBeInTheDocument()
    await user.clear(search)

    const modelRow = within(modal).getByRole('row', { name: /gpt-4o-mini/ })
    await user.selectOptions(within(modelRow).getByLabelText('gpt-4o-mini 测试协议'), 'responses')
    await user.click(within(modelRow).getByRole('button', { name: '测活 gpt-4o-mini' }))
    expect(await within(modelRow).findByText('健康')).toBeInTheDocument()
    expect(within(modelRow).getByText('842 ms')).toBeInTheDocument()

    await user.selectOptions(within(modal).getByLabelText('测活状态'), 'rate_limited')
    const limitedRow = within(modal).getByRole('row', { name: /gpt-4.1/ })
    expect(within(limitedRow).getByRole('button', { name: '重试 gpt-4.1' })).toBeInTheDocument()

    await user.click(within(modal).getByRole('button', { name: '刷新模型' }))
    expect(await within(modal).findByText('模型发现任务已提交')).toBeInTheDocument()
    expect(within(modal).getByText('部分模型刷新未完成：请求受限')).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('sk-primary')
    expect(document.body.textContent).not.toContain('请把')
  })

  it('loads the New API group snapshot and retries a failed request', async () => {
    let groupRequests = 0
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/v1/upstreams/source-user/keys') return envelope({ keys: [] })
      expect(path).toBe('/api/v1/upstreams/source-user/groups')
      groupRequests += 1
      if (groupRequests === 1) return failure('upstream_failure', '分组快照暂时不可用')
      return envelope({
        upstream_id: 'source-user',
        refreshed: true,
        groups: [{
          name: 'vip',
          description: '高优先级分组',
          ratio: 1.5,
          ratio_known: true,
          models: ['gpt-4.1', 'gpt-4o-mini'],
          model_count: 2,
          models_verified: true,
          auto: false,
        }],
      })
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    await renderDetail('upstream', 'source-user')

    await user.click(screen.getByRole('tab', { name: 'New API 分组' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('分组快照暂时不可用')
    await user.click(within(alert).getByRole('button', { name: '重试分组快照' }))

    const table = await screen.findByRole('table', { name: 'New API 分组列表' })
    const row = within(table).getByRole('row', { name: /vip/ })
    expect(within(row).getByText('高优先级分组')).toBeInTheDocument()
    expect(within(row).getByText('1.5x')).toBeInTheDocument()
    expect(within(row).getByText('2 个模型')).toBeInTheDocument()
    expect(within(row).getByText('已确证')).toBeInTheDocument()
    expect(groupRequests).toBe(2)
  })
})
