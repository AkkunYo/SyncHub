/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { createPinia } from 'pinia'
import { cleanup, render, screen, waitFor, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { useConsoleStore } from '@/stores/console'
import type { SanitizedConfig } from '@/types'

import TargetsPage from './TargetsPage.vue'
import UpstreamsPage from './UpstreamsPage.vue'

const upstreamsPageSource = readFileSync(resolve(process.cwd(), 'src/pages/UpstreamsPage.vue'), 'utf8')

function blockFor(source: string, selector: string): string {
  const selectorStart = source.indexOf(selector)
  if (selectorStart === -1) return ''
  const blockStart = source.indexOf('{', selectorStart + selector.length)
  if (blockStart === -1) return ''

  let depth = 1
  for (let index = blockStart + 1; index < source.length; index += 1) {
    if (source[index] === '{') depth += 1
    if (source[index] === '}') depth -= 1
    if (depth === 0) return source.slice(blockStart + 1, index)
  }
  return ''
}

function declarationsFor(source: string, selector: string): string {
  return blockFor(source, selector).replace(/\s+/g, ' ').trim()
}

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

function failure(code: string, message: string, status = 502): Response {
  return new Response(
    JSON.stringify({ success: false, error: { code, message }, request_id: 'req-ui-error' }),
    { status, headers: { 'Content-Type': 'application/json' } },
  )
}

async function renderPage(
  page: typeof UpstreamsPage | typeof TargetsPage,
  path: string,
  pageConfig: SanitizedConfig = config,
) {
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
  store.config = structuredClone(pageConfig)
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
  it('recovers from loading errors and clears all URL-backed filters', async () => {
    const user = userEvent.setup()
    const { router, store } = await renderPage(
      UpstreamsPage,
      '/upstreams?q=missing&type=generic&status=failed',
    )

    expect(screen.getByText('没有匹配的上游连接')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '清除筛选' }))
    await waitFor(() => expect(router.currentRoute.value.query).toEqual({}))
    expect(screen.getByText('New API 用户源')).toBeInTheDocument()
    expect(screen.getByText('通用生产源')).toBeInTheDocument()

    store.initialState = 'loading'
    expect(await screen.findByRole('status')).toHaveTextContent('正在加载上游连接')

    const reload = vi.spyOn(store, 'loadConsole').mockResolvedValue()
    store.initialError = ''
    store.initialState = 'error'
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('上游连接加载失败')
    await user.click(within(alert).getByRole('button', { name: '重试' }))
    expect(reload).toHaveBeenCalledOnce()
  })

  it('guides users through New API and generic connection validation errors', async () => {
    const user = userEvent.setup()
    await renderPage(UpstreamsPage, '/upstreams')

    await user.click(screen.getByRole('button', { name: '添加上游连接' }))
    const drawer = screen.getByRole('dialog', { name: '添加上游连接' })
    const save = within(drawer).getByRole('button', { name: '保存上游' })

    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('连接 ID 和名称不能为空')

    await user.type(within(drawer).getByLabelText('连接 ID'), 'bad id')
    await user.type(within(drawer).getByLabelText('连接名称'), '待校验连接')
    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('连接 ID 格式无效')

    await user.clear(within(drawer).getByLabelText('连接 ID'))
    await user.type(within(drawer).getByLabelText('连接 ID'), 'source-new')
    await user.type(within(drawer).getByLabelText('Base URL'), 'ftp://source.example.com')
    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('请输入不含凭证的绝对 HTTP(S) 地址')

    await user.clear(within(drawer).getByLabelText('Base URL'))
    await user.type(within(drawer).getByLabelText('Base URL'), 'https://source.example.com/')
    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('普通用户管理 Token 不能为空')

    await user.click(within(drawer).getByRole('radio', { name: '通用 OpenAI-compatible' }))
    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('第 1 个 Key 信息不完整')

    await user.type(within(drawer).getByLabelText('第 1 个 Key ID'), 'bad id')
    await user.type(within(drawer).getByLabelText('第 1 个 Key 别名'), '主 Key')
    await user.type(within(drawer).getByLabelText('第 1 个 API Key'), 'sk-primary')
    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('第 1 个 Key ID 格式无效')

    await user.clear(within(drawer).getByLabelText('第 1 个 Key ID'))
    await user.type(within(drawer).getByLabelText('第 1 个 Key ID'), 'primary')
    await user.click(within(drawer).getByRole('button', { name: '再添加一个 Key' }))
    await user.type(within(drawer).getByLabelText('第 2 个 Key ID'), 'primary')
    await user.type(within(drawer).getByLabelText('第 2 个 Key 别名'), '备用 Key')
    await user.type(within(drawer).getByLabelText('第 2 个 API Key'), 'sk-backup')
    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('Key ID 不能重复')

    await user.click(within(drawer).getByRole('button', { name: '移除第 2 个 Key' }))
    expect(within(drawer).queryByLabelText('第 2 个 API Key')).not.toBeInTheDocument()
  })

  it('saves a New API upstream with a numeric ordinary-user ID', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      expect(String(input)).toBe('/api/v1/upstreams')
      expect(init.method).toBe('POST')
      expect(JSON.parse(String(init.body))).toEqual({
        id: 'source-numbered',
        name: '带用户 ID 的上游',
        type: 'newapi',
        base_url: 'https://numbered-source.example.com',
        user_id: 23,
        access_token: 'ordinary-user-token',
      })
      return envelope({
        id: 'source-numbered',
        name: '带用户 ID 的上游',
        type: 'newapi',
        base_url: 'https://numbered-source.example.com',
        user_id: 23,
        sync_mappings: [],
      }, 201)
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    await renderPage(UpstreamsPage, '/upstreams')

    await user.click(screen.getByRole('button', { name: '添加上游连接' }))
    const drawer = screen.getByRole('dialog', { name: '添加上游连接' })
    await user.type(within(drawer).getByLabelText('连接 ID'), 'source-numbered')
    await user.type(within(drawer).getByLabelText('连接名称'), '带用户 ID 的上游')
    await user.type(within(drawer).getByLabelText('Base URL'), 'https://numbered-source.example.com/')
    await user.type(within(drawer).getByLabelText('New API 用户 ID'), '23')
    await user.type(within(drawer).getByLabelText('普通用户管理 Token'), 'ordinary-user-token')
    await user.click(within(drawer).getByRole('button', { name: '保存上游' }))

    expect(await screen.findByText('带用户 ID 的上游')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledOnce()
  })

  it('presents compact searchable rows with honest per-key summaries', async () => {
    const user = userEvent.setup()
    const summaryConfig = structuredClone(config)
    summaryConfig.upstreams.push({
      id: 'source-empty',
      name: '尚未配置 Key 的通用源',
      type: 'generic',
      base_url: 'https://empty.example.com/v1',
      keys: [],
      sync_mappings: [],
    })
    const { router } = await renderPage(UpstreamsPage, '/upstreams', summaryConfig)

    expect(screen.getByRole('heading', { name: '上游连接' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '添加上游连接' })).toBeInTheDocument()
    const newAPIRow = screen.getByRole('row', { name: /New API 用户源/ })
    expect(within(newAPIRow).getByRole('link', { name: '进入详情查看' })).toBeInTheDocument()
    expect(within(newAPIRow).queryByText('待发现')).not.toBeInTheDocument()

    const emptyKeyRow = screen.getByRole('row', { name: /尚未配置 Key 的通用源/ })
    expect(within(emptyKeyRow).getByText('0 Key')).toBeInTheDocument()
    expect(within(emptyKeyRow).getByText('0 个模型')).toBeInTheDocument()

    const genericRow = screen.getByRole('row', { name: /通用生产源/ })
    expect(within(genericRow).getByText('1 / 2 启用')).toBeInTheDocument()
    expect(within(genericRow).getByText('3 个模型')).toBeInTheDocument()
    expect(within(genericRow).getByText('未验证')).toBeInTheDocument()
    expect(within(genericRow).queryByText('健康')).not.toBeInTheDocument()
    expect(within(genericRow).getByText('验证', { selector: '.action-label' })).toBeInTheDocument()
    expect(within(genericRow).getByText('编辑', { selector: '.action-label' })).toBeInTheDocument()
    expect(within(genericRow).getByText('详情', { selector: '.action-label' })).toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('接入预设'), 'generic')
    expect(screen.queryByText('New API 用户源')).not.toBeInTheDocument()
    expect(screen.getByText('通用生产源')).toBeInTheDocument()
    await waitFor(() => expect(router.currentRoute.value.query.type).toBe('generic'))

    await user.clear(screen.getByRole('searchbox', { name: '搜索上游连接' }))
    await user.type(screen.getByRole('searchbox', { name: '搜索上游连接' }), '没有结果')
    expect(screen.getByText('没有匹配的上游连接')).toBeInTheDocument()
  })

  it.each([320, 390])('keeps upstream rows and controls scannable at %ipx', (viewportWidth) => {
    const breakpoint = 720
    const mobileRules = blockFor(upstreamsPageSource, `@media (max-width: ${breakpoint}px)`)
    const controls = declarationsFor(mobileRules, '.search-control input,')
    const cells = declarationsFor(mobileRules, '.connection-table td')
    const longValues = declarationsFor(mobileRules, '.primary-cell code,')
    const actions = declarationsFor(mobileRules, '.row-actions .icon-button')
    const actionLabels = declarationsFor(mobileRules, '.action-label')
    const surface = declarationsFor(upstreamsPageSource, '.connection-surface')
    const tableWrap = declarationsFor(upstreamsPageSource, '.table-wrap')

    expect(viewportWidth).toBeLessThanOrEqual(breakpoint)
    expect(controls).toContain('min-height: 44px;')
    expect(cells).toContain('grid-template-columns: 68px minmax(0, 1fr);')
    expect(mobileRules).toContain('.connection-table td::before')
    expect(upstreamsPageSource).toContain('data-label="端点"')
    expect(upstreamsPageSource).toContain('data-label="Key / 模型"')
    expect(upstreamsPageSource).toContain('data-label="验证"')
    expect(longValues).toContain('overflow-wrap: anywhere;')
    expect(longValues).toContain('white-space: normal;')
    expect(actions).toContain('min-height: 44px;')
    expect(actionLabels).toContain('display: inline;')
    expect(surface).toContain('min-height: 0;')
    expect(surface).toContain('flex: none;')
    expect(tableWrap).toContain('flex: none;')
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

  it('shows failed validation and supports retrying edits and confirmed deletion', async () => {
    let updateAttempts = 0
    let deleteAttempts = 0
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const path = String(input)
      if (path.endsWith('/connection-tests')) {
        return failure('upstream_failure', '上游暂时不可达')
      }
      if (init.method === 'PUT') {
        updateAttempts += 1
        if (updateAttempts === 1) return failure('upstream_failure', '保存上游失败')
        return envelope({ ...config.upstreams[1], name: '通用生产源（已更新）' })
      }
      expect(init.method).toBe('DELETE')
      deleteAttempts += 1
      return deleteAttempts === 1
        ? failure('upstream_failure', '删除上游失败')
        : envelope({})
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    await renderPage(UpstreamsPage, '/upstreams')

    await user.click(screen.getByRole('button', { name: '验证 通用生产源 连接' }))
    const failedRow = screen.getByRole('row', { name: /通用生产源/ })
    expect(await within(failedRow).findByText('验证失败')).toBeInTheDocument()
    expect(within(failedRow).getByText('上游暂时不可达')).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('验证状态'), 'failed')
    expect(screen.queryByText('New API 用户源')).not.toBeInTheDocument()

    await user.click(within(failedRow).getByRole('button', { name: '编辑 通用生产源' }))
    let drawer = screen.getByRole('dialog', { name: '编辑上游连接' })
    const name = within(drawer).getByLabelText('连接名称')
    await user.clear(name)
    await user.type(name, '通用生产源（已更新）')
    await user.click(within(drawer).getByRole('button', { name: '保存上游' }))
    expect(await within(drawer).findByRole('alert')).toHaveTextContent('保存上游失败')
    await user.click(within(drawer).getByRole('button', { name: '保存上游' }))
    expect(await screen.findByText('通用生产源（已更新）')).toBeInTheDocument()

    const updatedRow = screen.getByRole('row', { name: /通用生产源（已更新）/ })
    await user.click(within(updatedRow).getByRole('button', { name: '编辑 通用生产源（已更新）' }))
    drawer = screen.getByRole('dialog', { name: '编辑上游连接' })
    await user.click(within(drawer).getByRole('button', { name: '删除连接' }))
    expect(within(drawer).getByRole('button', { name: '确认删除连接' })).toBeInTheDocument()
    await user.click(within(drawer).getByRole('button', { name: '确认删除连接' }))
    expect(await within(drawer).findByRole('alert')).toHaveTextContent('删除上游失败')
    await user.click(within(drawer).getByRole('button', { name: '确认删除连接' }))
    await waitFor(() => expect(screen.queryByText('通用生产源（已更新）')).not.toBeInTheDocument())
  })
})

describe('Target instances workspace', () => {
  it('recovers from loading errors and clears target URL filters', async () => {
    const user = userEvent.setup()
    const { router, store } = await renderPage(
      TargetsPage,
      '/targets?q=missing&type=cliproxyapi&status=verified',
    )

    expect(screen.getByText('没有匹配的目标实例')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '清除筛选' }))
    await waitFor(() => expect(router.currentRoute.value.query).toEqual({}))
    expect(screen.getByText('生产 New API')).toBeInTheDocument()
    expect(screen.getByText('备用 CPA')).toBeInTheDocument()

    store.initialState = 'loading'
    expect(await screen.findByRole('status')).toHaveTextContent('正在加载目标实例')

    const reload = vi.spyOn(store, 'loadConsole').mockResolvedValue()
    store.initialError = '目标配置读取失败'
    store.initialState = 'error'
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('目标配置读取失败')
    await user.click(within(alert).getByRole('button', { name: '重试' }))
    expect(reload).toHaveBeenCalledOnce()
  })

  it('guides users through target identity, endpoint, credential, and user validation', async () => {
    const user = userEvent.setup()
    await renderPage(TargetsPage, '/targets')

    await user.click(screen.getByRole('button', { name: '添加目标实例' }))
    const drawer = screen.getByRole('dialog', { name: '添加目标实例' })
    const save = within(drawer).getByRole('button', { name: '保存目标' })

    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('实例 ID 和名称不能为空')
    await user.type(within(drawer).getByLabelText('实例 ID'), 'bad id')
    await user.type(within(drawer).getByLabelText('实例名称'), '待校验目标')
    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('实例 ID 格式无效')

    await user.clear(within(drawer).getByLabelText('实例 ID'))
    await user.type(within(drawer).getByLabelText('实例 ID'), 'target-new')
    await user.type(within(drawer).getByLabelText('Base URL'), 'https://admin:secret@target.example.com')
    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('请输入不含凭证的绝对 HTTP(S) 地址')

    await user.clear(within(drawer).getByLabelText('Base URL'))
    await user.type(within(drawer).getByLabelText('Base URL'), 'https://target.example.com/')
    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('New API 管理员 Token 不能为空')

    await user.click(within(drawer).getByRole('radio', { name: 'CPA' }))
    await user.click(save)
    expect(within(drawer).getByRole('alert')).toHaveTextContent('CPA 管理员凭证不能为空')
  })

  it('saves a New API target with a numeric administrator user ID', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      expect(String(input)).toBe('/api/v1/targets')
      expect(init.method).toBe('POST')
      expect(JSON.parse(String(init.body))).toEqual({
        id: 'target-numbered',
        name: '带用户 ID 的目标',
        type: 'newapi',
        base_url: 'https://numbered-target.example.com',
        user_id: 42,
        access_token: 'administrator-token',
      })
      return envelope({
        id: 'target-numbered',
        name: '带用户 ID 的目标',
        type: 'newapi',
        base_url: 'https://numbered-target.example.com',
        user_id: 42,
      }, 201)
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    await renderPage(TargetsPage, '/targets')

    await user.click(screen.getByRole('button', { name: '添加目标实例' }))
    const drawer = screen.getByRole('dialog', { name: '添加目标实例' })
    await user.type(within(drawer).getByLabelText('实例 ID'), 'target-numbered')
    await user.type(within(drawer).getByLabelText('实例名称'), '带用户 ID 的目标')
    await user.type(within(drawer).getByLabelText('Base URL'), 'https://numbered-target.example.com/')
    await user.type(within(drawer).getByLabelText('New API 用户 ID'), '42')
    await user.type(within(drawer).getByLabelText('New API 管理员 Token'), 'administrator-token')
    await user.click(within(drawer).getByRole('button', { name: '保存目标' }))

    expect(await screen.findByText('带用户 ID 的目标')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledOnce()
  })

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

  it('restores target validation summaries and capabilities from sanitized config', async () => {
    const validatedConfig = structuredClone(config)
    validatedConfig.targets[0] = {
      ...validatedConfig.targets[0]!,
      validation_status: 'verified',
      validated_at: '2026-08-06T10:00:00Z',
      validation_capabilities: { supports_static_key: true, supports_proxy_endpoint: false },
    }
    await renderPage(TargetsPage, '/targets', validatedConfig)

    const row = screen.getByRole('row', { name: /生产 New API/ })
    expect(within(row).getByText('验证通过')).toBeInTheDocument()
    expect(within(row).getByText(/supports_static_key/)).toBeInTheDocument()
    expect(within(row).getByText(/supports_proxy_endpoint/)).toBeInTheDocument()
  })

  it('preserves the persisted target summary when a list connection test fails', async () => {
    const validatedConfig = structuredClone(config)
    const persistedTarget = {
      ...validatedConfig.targets[1]!,
      validation_status: 'verified' as const,
      validated_at: '2026-08-06T10:00:00Z',
      validation_capabilities: {
        platform: 'cliproxyapi',
        providers: { openai: { modes: ['static_key'] } },
      },
    }
    validatedConfig.targets[1] = persistedTarget
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      failure('upstream_failure', '目标管理端点不可达'),
    ))
    const user = userEvent.setup()
    const { store } = await renderPage(TargetsPage, '/targets', validatedConfig)

    const row = screen.getByRole('row', { name: /备用 CPA/ })
    expect(within(row).getByText('验证通过')).toBeInTheDocument()
    expect(within(row).getByText(/平台: cliproxyapi/)).toBeInTheDocument()
    await user.click(within(row).getByRole('button', { name: '验证 备用 CPA 连接' }))

    expect(await within(row).findByText('验证失败')).toBeInTheDocument()
    expect(within(row).getByText('目标管理端点不可达')).toBeInTheDocument()
    expect(store.targets.find((target) => target.id === persistedTarget.id)).toMatchObject(persistedTarget)
    expect(within(row).getByText(/平台: cliproxyapi/)).toBeInTheDocument()
  })

  it('shows failed target validation and retries edits and confirmed deletion', async () => {
    let updateAttempts = 0
    let deleteAttempts = 0
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const path = String(input)
      if (path.endsWith('/connection-tests')) {
        return failure('upstream_failure', '目标管理端点不可达')
      }
      if (init.method === 'PUT') {
        updateAttempts += 1
        if (updateAttempts === 1) return failure('upstream_failure', '保存目标失败')
        return envelope({ ...config.targets[1], name: '备用 CPA（已更新）' })
      }
      expect(init.method).toBe('DELETE')
      deleteAttempts += 1
      return deleteAttempts === 1
        ? failure('upstream_failure', '删除目标失败')
        : envelope({})
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    await renderPage(TargetsPage, '/targets')

    await user.click(screen.getByRole('button', { name: '验证 备用 CPA 连接' }))
    const failedRow = screen.getByRole('row', { name: /备用 CPA/ })
    expect(await within(failedRow).findByText('验证失败')).toBeInTheDocument()
    expect(within(failedRow).getByText('目标管理端点不可达')).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('验证状态'), 'failed')
    expect(screen.queryByText('生产 New API')).not.toBeInTheDocument()

    await user.click(within(failedRow).getByRole('button', { name: '编辑 备用 CPA' }))
    let drawer = screen.getByRole('dialog', { name: '编辑目标实例' })
    const name = within(drawer).getByLabelText('实例名称')
    await user.clear(name)
    await user.type(name, '备用 CPA（已更新）')
    await user.click(within(drawer).getByRole('button', { name: '保存目标' }))
    expect(await within(drawer).findByRole('alert')).toHaveTextContent('保存目标失败')
    await user.click(within(drawer).getByRole('button', { name: '保存目标' }))
    expect(await screen.findByText('备用 CPA（已更新）')).toBeInTheDocument()

    const updatedRow = screen.getByRole('row', { name: /备用 CPA（已更新）/ })
    await user.click(within(updatedRow).getByRole('button', { name: '编辑 备用 CPA（已更新）' }))
    drawer = screen.getByRole('dialog', { name: '编辑目标实例' })
    await user.click(within(drawer).getByRole('button', { name: '删除实例' }))
    expect(within(drawer).getByRole('button', { name: '确认删除实例' })).toBeInTheDocument()
    await user.click(within(drawer).getByRole('button', { name: '确认删除实例' }))
    expect(await within(drawer).findByRole('alert')).toHaveTextContent('删除目标失败')
    await user.click(within(drawer).getByRole('button', { name: '确认删除实例' }))
    await waitFor(() => expect(screen.queryByText('备用 CPA（已更新）')).not.toBeInTheDocument())
  })
})
