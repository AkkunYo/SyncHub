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
  upstreams: [genericSource],
} as SanitizedConfig

function envelope(data: unknown, status = 200): Response {
  return new Response(JSON.stringify({ success: true, data, request_id: 'req-detail' }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

async function renderDetail(kind: 'upstream' | 'target') {
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
  await router.push(kind === 'upstream' ? '/upstreams/source-generic' : '/targets/target-main')
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
  })

  it('adds a write-only key from the generic connection detail drawer', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      expect(String(input)).toBe('/api/v1/upstreams/source-generic/keys')
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
})
