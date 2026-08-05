import { createPinia } from 'pinia'
import { cleanup, render, screen, waitFor, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { useConsoleStore } from '@/stores/console'
import type { SanitizedConfig } from '@/types'

import TargetsPage from './TargetsPage.vue'
import UpstreamsPage from './UpstreamsPage.vue'

const config = {
  app: {
    host: '127.0.0.1',
    port: 8888,
    reconcile_interval: '5m0s',
    request_timeout: '15s',
    sync_concurrency: 4,
  },
  targets: [
    {
      id: 'target-main',
      name: '生产 New API',
      type: 'newapi',
      base_url: 'https://target.example.com',
      user_id: 9,
    },
    {
      id: 'target-cpa',
      name: '备用 CPA',
      type: 'cliproxyapi',
      base_url: 'https://cpa.example.com',
    },
  ],
  upstreams: [
    {
      id: 'source-user',
      name: 'New API 用户源',
      type: 'newapi',
      base_url: 'https://source.example.com',
      user_id: 17,
      sync_mappings: [],
    },
    {
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
        {
          id: 'backup',
          name: '备用 Key',
          enabled: false,
          models: ['gpt-4.1'],
          credential_present: true,
        },
      ],
      sync_mappings: [],
    },
  ],
} as SanitizedConfig

function envelope(data: unknown, status = 200): Response {
  return new Response(JSON.stringify({ success: true, data, request_id: 'req-ui' }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

async function renderPage(page: typeof UpstreamsPage | typeof TargetsPage, path: string) {
  const pinia = createPinia()
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/upstreams', name: 'upstreams', component: UpstreamsPage },
      { path: '/upstreams/:id', name: 'upstream-detail', component: { template: '<div />' } },
      { path: '/targets', name: 'targets', component: TargetsPage },
      { path: '/targets/:id', name: 'target-detail', component: { template: '<div />' } },
      { path: '/targets/:id/channels', name: 'target-channels', component: { template: '<div />' } },
      { path: '/sync', name: 'sync', component: { template: '<div />' } },
    ],
  })
  const store = useConsoleStore(pinia)
  store.config = structuredClone(config)
  store.initialState = 'ready'
  store.selectedUpstreamId = config.upstreams[0]?.id ?? ''
  store.selectedTargetId = config.targets[0]?.id ?? ''
  await router.push(path)
  await router.isReady()
  return { ...render(page, { global: { plugins: [pinia, router] } }), router, store }
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('Upstream connections workspace', () => {
  it('presents compact searchable rows with honest per-key summaries', async () => {
    const user = userEvent.setup()
    const { router } = await renderPage(UpstreamsPage, '/upstreams')

    expect(screen.getByRole('heading', { name: '上游连接' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '添加上游连接' })).toBeInTheDocument()
    const genericRow = screen.getByRole('row', { name: /通用生产源/ })
    expect(within(genericRow).getByText('1 / 2 启用')).toBeInTheDocument()
    expect(within(genericRow).getByText('3 个模型')).toBeInTheDocument()
    expect(within(genericRow).getByText('未验证')).toBeInTheDocument()
    expect(within(genericRow).queryByText('健康')).not.toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('接入预设'), 'generic')
    expect(screen.queryByText('New API 用户源')).not.toBeInTheDocument()
    expect(screen.getByText('通用生产源')).toBeInTheDocument()
    await waitFor(() => expect(router.currentRoute.value.query.type).toBe('generic'))

    await user.clear(screen.getByRole('searchbox', { name: '搜索上游连接' }))
    await user.type(screen.getByRole('searchbox', { name: '搜索上游连接' }), '没有结果')
    expect(screen.getByText('没有匹配的上游连接')).toBeInTheDocument()
  })

  it('creates a generic connection with multiple write-only keys in a side drawer', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      expect(String(input)).toBe('/api/v1/upstreams')
      expect(init.method).toBe('POST')
      const payload = JSON.parse(String(init.body)) as Record<string, unknown>
      expect(payload).toMatchObject({
        id: 'source-new',
        name: '新通用源',
        type: 'generic',
        base_url: 'https://new-source.example.com/v1',
        keys: [
          { id: 'primary', name: '主 Key', api_key: 'sk-primary', enabled: true },
          { id: 'backup', name: '备用 Key', api_key: 'sk-backup', enabled: true },
        ],
      })
      expect(payload).not.toHaveProperty('api_key')
      return envelope({
        id: 'source-new',
        name: '新通用源',
        type: 'generic',
        base_url: 'https://new-source.example.com/v1',
        keys: [
          { id: 'primary', name: '主 Key', enabled: true, models: [], credential_present: true },
          { id: 'backup', name: '备用 Key', enabled: true, models: [], credential_present: true },
        ],
        sync_mappings: [],
      }, 201)
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    await renderPage(UpstreamsPage, '/upstreams')

    await user.click(screen.getByRole('button', { name: '添加上游连接' }))
    const drawer = screen.getByRole('dialog', { name: '添加上游连接' })
    expect(drawer).toHaveAttribute('data-side', 'right')
    await user.click(within(drawer).getByRole('radio', { name: '通用 OpenAI-compatible' }))
    await user.type(within(drawer).getByLabelText('连接 ID'), 'source-new')
    await user.type(within(drawer).getByLabelText('连接名称'), '新通用源')
    await user.type(within(drawer).getByLabelText('Base URL'), 'https://new-source.example.com/v1')
    await user.type(within(drawer).getByLabelText('第 1 个 Key ID'), 'primary')
    await user.type(within(drawer).getByLabelText('第 1 个 Key 别名'), '主 Key')
    await user.type(within(drawer).getByLabelText('第 1 个 API Key'), 'sk-primary')
    await user.click(within(drawer).getByRole('button', { name: '再添加一个 Key' }))
    await user.type(within(drawer).getByLabelText('第 2 个 Key ID'), 'backup')
    await user.type(within(drawer).getByLabelText('第 2 个 Key 别名'), '备用 Key')
    await user.type(within(drawer).getByLabelText('第 2 个 API Key'), 'sk-backup')
    await user.click(within(drawer).getByRole('button', { name: '保存上游' }))

    expect(await screen.findByText('新通用源')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(screen.queryByDisplayValue('sk-primary')).not.toBeInTheDocument()
    expect(screen.queryByDisplayValue('sk-backup')).not.toBeInTheDocument()
  })

  it('labels New API credentials as ordinary-user tokens and runs non-destructive validation', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      expect(String(input)).toBe('/api/v1/upstreams/source-user/connection-tests')
      expect(init.method).toBe('POST')
      return envelope({
        reachable: true,
        authenticated: true,
        authorized: true,
        resource_count: 4,
        capabilities: { can_list_assets: true },
      })
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    await renderPage(UpstreamsPage, '/upstreams')

    await user.click(screen.getByRole('button', { name: '添加上游连接' }))
    const drawer = screen.getByRole('dialog', { name: '添加上游连接' })
    expect(within(drawer).getByRole('radio', { name: 'New API 普通用户' })).toBeChecked()
    expect(within(drawer).getByLabelText('普通用户管理 Token')).toHaveAttribute('type', 'password')
    expect(within(drawer).queryByText(/管理员 Token/)).not.toBeInTheDocument()
    await user.click(within(drawer).getByRole('button', { name: '关闭添加上游连接' }))

    await user.click(screen.getByRole('button', { name: '验证 New API 用户源 连接' }))
    const row = screen.getByRole('row', { name: /New API 用户源/ })
    expect(await within(row).findByText('验证通过')).toBeInTheDocument()
    expect(within(row).getByText('4 个资源')).toBeInTheDocument()
  })
})

describe('Target instances workspace', () => {
  it('limits target presets to New API and CPA and persists filters in the URL', async () => {
    const user = userEvent.setup()
    const { router } = await renderPage(TargetsPage, '/targets')

    expect(screen.getByRole('button', { name: '添加目标实例' })).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('平台类型'), 'cliproxyapi')
    expect(screen.queryByText('生产 New API')).not.toBeInTheDocument()
    expect(screen.getByText('备用 CPA')).toBeInTheDocument()
    await waitFor(() => expect(router.currentRoute.value.query.type).toBe('cliproxyapi'))

    await user.click(screen.getByRole('button', { name: '添加目标实例' }))
    const drawer = screen.getByRole('dialog', { name: '添加目标实例' })
    expect(within(drawer).getByRole('radio', { name: 'New API' })).toBeInTheDocument()
    expect(within(drawer).getByRole('radio', { name: 'CPA' })).toBeInTheDocument()
    expect(within(drawer).queryByRole('radio', { name: /通用/ })).not.toBeInTheDocument()
  })

  it('saves an administrator target and validates it through the read-only connection route', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const path = String(input)
      if (path === '/api/v1/targets') {
        const payload = JSON.parse(String(init.body)) as Record<string, unknown>
        expect(payload).toEqual({
          id: 'target-new',
          name: '新 CPA 目标',
          type: 'cliproxyapi',
          base_url: 'https://new-cpa.example.com',
          management_key: 'cpa-secret',
        })
        return envelope({
          id: 'target-new',
          name: '新 CPA 目标',
          type: 'cliproxyapi',
          base_url: 'https://new-cpa.example.com',
        }, 201)
      }
      expect(path).toBe('/api/v1/targets/target-new/connection-tests')
      expect(init.method).toBe('POST')
      return envelope({
        reachable: true,
        authenticated: true,
        authorized: true,
        resource_count: 12,
        capabilities: { supports_static_key: true },
      })
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    await renderPage(TargetsPage, '/targets')

    await user.click(screen.getByRole('button', { name: '添加目标实例' }))
    const drawer = screen.getByRole('dialog', { name: '添加目标实例' })
    await user.click(within(drawer).getByRole('radio', { name: 'CPA' }))
    await user.type(within(drawer).getByLabelText('实例 ID'), 'target-new')
    await user.type(within(drawer).getByLabelText('实例名称'), '新 CPA 目标')
    await user.type(within(drawer).getByLabelText('Base URL'), 'https://new-cpa.example.com')
    await user.type(within(drawer).getByLabelText('CPA 管理员凭证'), 'cpa-secret')
    await user.click(within(drawer).getByRole('button', { name: '保存目标' }))

    const newRow = await screen.findByRole('row', { name: /新 CPA 目标/ })
    expect(within(newRow).getByText('未验证')).toBeInTheDocument()
    expect(screen.queryByDisplayValue('cpa-secret')).not.toBeInTheDocument()
    await user.click(within(newRow).getByRole('button', { name: '验证 新 CPA 目标 连接' }))
    expect(await within(newRow).findByText('验证通过')).toBeInTheDocument()
    expect(within(newRow).getByText('12 个渠道')).toBeInTheDocument()
  })
})
