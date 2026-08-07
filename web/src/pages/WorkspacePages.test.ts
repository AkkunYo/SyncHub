/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { createPinia, setActivePinia } from 'pinia'
import { render, screen, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/api/client'
import { useConsoleStore } from '@/stores/console'
import type { Channel, MatrixData, SanitizedConfig } from '@/types'
import ChannelsPage from './ChannelsPage.vue'
import DriftPage from './DriftPage.vue'
import MatrixPage from './MatrixPage.vue'
import SettingsPage from './SettingsPage.vue'
import TasksPage from './TasksPage.vue'

const appSettings = {
  host: '127.0.0.1',
  port: 8888,
  reconcile_interval: '5m0s',
  request_timeout: '15s',
  sync_concurrency: 4,
}
const target = {
  id: 'target-a',
  name: 'Target Alpha',
  type: 'newapi' as const,
  base_url: 'https://target.invalid',
}
const upstream = {
  id: 'source-a',
  name: 'Source Alpha',
  type: 'newapi' as const,
  base_url: 'https://source.invalid',
}
const configured: SanitizedConfig = {
  app: appSettings,
  targets: [target],
  upstreams: [upstream],
}
const driftMatrix: MatrixData = {
  upstream_id: upstream.id,
  refreshed: true,
  targets: [target],
  rows: [{
    asset: {
      id: 'asset-a',
      source_id: upstream.id,
      source_type: 'newapi',
      provider: 'openai',
      raw_type: 'OpenAI',
      kind: 'static_api_key',
      name: 'Primary key',
      base_url: 'https://provider.invalid',
      models: ['gpt-4.1'],
      enabled: true,
      secret_readable: true,
      metadata: {},
    },
    cells: [{
      target_id: target.id,
      status: 'drifted',
      channel_id: '42',
      differences: [{ field: 'weight', expected: 100, actual: 80 }],
    }],
  }],
}
const channels: Channel[] = [
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
    upstream_asset_id: 'asset-a',
  },
  {
    id: '43',
    name: 'Native channel',
    provider: 'openai',
    raw_type: '1',
    base_url: 'https://native.invalid',
    models: ['gpt-4.1-mini'],
    group: 'default',
    priority: 0,
    weight: 100,
    enabled: false,
    managed: false,
  },
]

const routerLinkStub = {
  props: ['to'],
  template: '<a :href="to"><slot /></a>',
}

function setupStore(config: SanitizedConfig = configured) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useConsoleStore()
  store.config = config
  store.initialState = 'ready'
  store.matrixState = 'ready'
  store.channelState = 'ready'
  store.selectedUpstreamId = config.upstreams[0]?.id ?? ''
  store.selectedTargetId = config.targets[0]?.id ?? ''
  return { pinia, store }
}

describe('workspace page information architecture', () => {
  beforeEach(() => {
    window.localStorage.clear()
    window.sessionStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('turns the empty matrix into a short first-sync checklist', () => {
    const { pinia } = setupStore({ ...configured, targets: [], upstreams: [] })
    render(MatrixPage, {
      global: { plugins: [pinia], stubs: { RouterLink: routerLinkStub } },
    })

    expect(screen.getByRole('heading', { name: '资产矩阵' })).toHaveTextContent('同步工作台')
    const checklist = screen.getByRole('list', { name: '首次同步步骤' })
    expect(within(checklist).getAllByRole('listitem')).toHaveLength(5)
    expect(checklist).toHaveTextContent('配置目标实例')
    expect(checklist).toHaveTextContent('验证目标实例')
    expect(checklist).toHaveTextContent('配置上游连接')
    expect(checklist).toHaveTextContent('刷新来源资产')
    expect(checklist).toHaveTextContent('选择资产并同步')
    expect(screen.getByRole('link', { name: '配置目标实例' })).toHaveAttribute('href', '/targets')
    expect(screen.getByRole('link', { name: '配置上游连接' })).toHaveAttribute('href', '/upstreams')
    expect(screen.queryByRole('button', { name: '前往设置' })).not.toBeInTheDocument()
  })

  it('tracks adding and validating a target as separate first-sync steps', () => {
    const { pinia } = setupStore({ ...configured, upstreams: [] })
    render(MatrixPage, {
      global: { plugins: [pinia], stubs: { RouterLink: routerLinkStub } },
    })

    const checklist = screen.getByRole('list', { name: '首次同步步骤' })
    expect(within(checklist).getByText('配置目标实例').closest('li')).toHaveClass('complete')
    expect(within(checklist).getByText('验证目标实例').closest('li')).not.toHaveClass('complete')
    expect(screen.getByText('1 / 5 已完成')).toBeInTheDocument()
  })

  it('routes missing channel prerequisites to target management', () => {
    const { pinia } = setupStore({ ...configured, targets: [] })
    render(ChannelsPage, {
      global: { plugins: [pinia], stubs: { RouterLink: routerLinkStub } },
    })

    expect(screen.getByRole('heading', { name: '尚未配置目标实例' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '管理目标实例' })).toHaveAttribute('href', '/targets')
  })

  it('summarizes managed and native target channels without changing table semantics', () => {
    const { pinia, store } = setupStore()
    store.channels = channels
    render(ChannelsPage, { global: { plugins: [pinia] } })

    const summary = screen.getByRole('region', { name: '渠道概览' })
    expect(summary).toHaveTextContent('2 个渠道')
    expect(summary).toHaveTextContent('1 个托管')
    expect(summary).toHaveTextContent('1 个原生')
    expect(screen.getByRole('table')).toBeInTheDocument()
  })

  it('presents drift as a pending scan queue while retaining its accessible heading', () => {
    const { pinia, store } = setupStore()
    store.matrix = driftMatrix
    render(DriftPage, { global: { plugins: [pinia] } })

    expect(screen.getByRole('heading', { name: '配置漂移' })).toHaveTextContent('漂移修复')
    expect(screen.getByText('当前上游：Source Alpha')).toBeInTheDocument()
    expect(screen.getByRole('toolbar', { name: '漂移扫描' })).toHaveTextContent('1 项待处理')
    const scanButton = screen.getByRole('button', { name: '校验全部目标' })
    expect(scanButton).toHaveTextContent('扫描漂移')
    expect(scanButton).toBeEnabled()

    const driftItem = screen.getByRole('article', { name: 'Primary key / Target Alpha 配置漂移' })
    expect(within(driftItem).getByText('期望值')).toBeInTheDocument()
    expect(within(driftItem).getByText('当前值')).toBeInTheDocument()
    expect(within(driftItem).getByText('100')).toBeInTheDocument()
    expect(within(driftItem).getByText('80')).toBeInTheDocument()
    expect(within(driftItem).getByText('100 -> 80')).toHaveClass('sr-only')
    expect(driftItem).toHaveTextContent('采纳后将以目标平台当前值作为后续同步基线。')
  })

  it('explains what the latest clean drift scan means', () => {
    const { pinia, store } = setupStore()
    store.matrix = { ...driftMatrix, rows: [] }
    render(DriftPage, { global: { plugins: [pinia] } })

    expect(screen.getByText('当前没有配置漂移')).toBeInTheDocument()
    expect(screen.getByText('最近一次扫描未发现差异，当前配置与目标状态一致。')).toBeInTheDocument()
    expect(screen.getByRole('toolbar', { name: '漂移扫描' })).toHaveTextContent('0 项待处理')
  })

  it('routes missing drift prerequisites to their owning connection pages', () => {
    const upstreamSetup = setupStore({ ...configured, upstreams: [] })
    const upstreamView = render(DriftPage, {
      global: { plugins: [upstreamSetup.pinia], stubs: { RouterLink: routerLinkStub } },
    })

    expect(screen.getByRole('link', { name: '管理上游连接' })).toHaveAttribute('href', '/upstreams')
    upstreamView.unmount()

    const targetSetup = setupStore({ ...configured, targets: [] })
    render(DriftPage, {
      global: { plugins: [targetSetup.pinia], stubs: { RouterLink: routerLinkStub } },
    })

    expect(screen.getByRole('link', { name: '管理目标实例' })).toHaveAttribute('href', '/targets')
  })

  it('uses a guided task empty state without rendering an empty desktop table', () => {
    render(TasksPage, {
      props: { tasks: [] },
      global: { stubs: { RouterLink: routerLinkStub } },
    })

    const emptyState = screen.getByRole('status', { name: '暂无任务记录' })
    expect(emptyState).toHaveTextContent('同步或校验任务执行后，会在这里保留状态与时间记录。')
    const workspaceLink = within(emptyState).getByRole('link', { name: '返回同步工作台' })
    expect(workspaceLink).toHaveTextContent('前往同步工作台')
    expect(workspaceLink).toHaveAttribute('href', '/sync')
    expect(screen.queryByRole('table', { name: '任务状态列表' })).not.toBeInTheDocument()
  })

  it('groups system settings by operational purpose and starts pristine', () => {
    const { pinia } = setupStore()
    render(SettingsPage, { global: { plugins: [pinia] } })

    expect(screen.getByRole('heading', { name: '设置' })).toHaveTextContent('系统设置')
    expect(screen.getByRole('region', { name: '网络监听' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '任务调度' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '运行参数' })).toBeInTheDocument()
    expect(screen.getByLabelText('监听地址')).toHaveValue('127.0.0.1')
    expect(screen.getByLabelText('端口')).toHaveValue(8888)
    expect(screen.getByLabelText('校验间隔')).toHaveValue('5m0s')
    expect(screen.getByLabelText('请求超时')).toHaveValue('15s')
    expect(screen.getByLabelText('同步并发')).toHaveValue(4)
    expect(screen.getByText('修改监听地址或端口后，可能需要重启服务并重新连接。')).toBeInTheDocument()
    expect(screen.getByText('支持 s、m、h 等时长单位，例如 5m0s。')).toBeInTheDocument()
    const saveButton = screen.getByRole('button', { name: '保存运行设置' })
    expect(saveButton).toHaveTextContent('保存设置')
    expect(saveButton).toBeDisabled()
    expect(screen.getByRole('button', { name: '重置修改' })).toBeDisabled()
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '添加目标实例' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '添加上游实例' })).not.toBeInTheDocument()
  })

  it('enables settings actions only for changes and returns to pristine after reset or save', async () => {
    const { pinia } = setupStore()
    const updateApp = vi.spyOn(api, 'updateApp').mockResolvedValue({
      ...appSettings,
      host: '0.0.0.0',
    })
    const user = userEvent.setup()
    render(SettingsPage, { global: { plugins: [pinia] } })

    const host = screen.getByLabelText('监听地址')
    const save = screen.getByRole('button', { name: '保存运行设置' })
    const reset = screen.getByRole('button', { name: '重置修改' })

    await user.clear(host)
    await user.type(host, '0.0.0.0')
    expect(save).toBeEnabled()
    expect(reset).toBeEnabled()

    await user.click(reset)
    expect(host).toHaveValue('127.0.0.1')
    expect(save).toBeDisabled()
    expect(reset).toBeDisabled()

    await user.clear(host)
    await user.type(host, '0.0.0.0')
    await user.click(save)

    expect(updateApp).toHaveBeenCalledWith(expect.objectContaining({ host: '0.0.0.0' }))
    expect(await screen.findByRole('status')).toHaveTextContent('运行设置已保存')
    expect(save).toBeDisabled()
    expect(reset).toBeDisabled()
  })
})

describe('workspace page responsive layout contracts', () => {
  const pageSources = [
    'MatrixPage.vue',
    'ChannelsPage.vue',
    'DriftPage.vue',
    'TasksPage.vue',
    'SettingsPage.vue',
  ].map((file) => readFileSync(resolve(process.cwd(), 'src/pages', file), 'utf8'))

  it('keeps all workspace layout CSS scoped to its page', () => {
    for (const source of pageSources) expect(source).toContain('<style scoped>')
  })

  it('keeps the sync selection dock sticky and mobile-safe', () => {
    const matrixPage = pageSources[0]
    expect(matrixPage).toContain('class="selection-dock"')
    expect(matrixPage).toMatch(/\.selection-dock\s*\{[^}]*position:\s*sticky;/s)
    expect(matrixPage).toContain('env(safe-area-inset-bottom)')
  })

  it('defines page-owned mobile information-row layouts', () => {
    for (const source of pageSources.slice(0, 4)) {
      expect(source).toContain('@media (max-width: 620px)')
    }
  })

  it('switches task history to a divided mobile list and wraps long task identifiers', () => {
    const tasksPage = pageSources[3]
    expect(tasksPage).toContain('class="tasks-mobile-list"')
    expect(tasksPage).toMatch(/\.task-id\s*\{[^}]*overflow-wrap:\s*anywhere;/s)
    expect(tasksPage).toMatch(/@media \(max-width: 620px\)[\s\S]*\.tasks-table-scroll\s*\{[^}]*display:\s*none;/)
    expect(tasksPage).toMatch(/@media \(max-width: 620px\)[\s\S]*\.tasks-mobile-list\s*\{[^}]*display:\s*block;/)
  })

  it('removes card framing from settings sections', () => {
    const settingsPage = pageSources[4]
    expect(settingsPage).toMatch(/\.settings-band\s*\{[^}]*border:\s*0;/s)
    expect(settingsPage).toMatch(/\.settings-band\s*\{[^}]*box-shadow:\s*none;/s)
  })

  it('keeps connection CRUD and dialogs out of system settings', () => {
    const settingsPage = pageSources[4]
    expect(settingsPage).not.toContain('ModalDialog')
    expect(settingsPage).not.toContain('settings-instances')
    expect(settingsPage).not.toMatch(/api\.(create|update|delete)(Target|Upstream)/)
  })
})
