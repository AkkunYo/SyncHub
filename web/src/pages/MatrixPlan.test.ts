import { createPinia, setActivePinia } from 'pinia'
import { render, screen, waitFor, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { useConsoleStore } from '@/stores/console'
import type { MatrixData, SanitizedConfig } from '@/types'

import MatrixPage from './MatrixPage.vue'

const config: SanitizedConfig = {
  app: {
    host: '127.0.0.1',
    port: 8888,
    reconcile_interval: '5m0s',
    request_timeout: '15s',
    sync_concurrency: 4,
  },
  targets: [
    {
      id: 'target-a', name: 'Target Alpha', type: 'newapi', base_url: 'https://target-a.invalid',
      validation_status: 'verified',
      validation_capabilities: { supports_static_key: true },
    },
    {
      id: 'target-b', name: 'Target Beta', type: 'cliproxyapi', base_url: 'https://target-b.invalid',
      validation_status: 'verified',
    },
  ],
  upstreams: [{ id: 'source-a', name: 'Source Alpha', type: 'generic', base_url: 'https://source.invalid' }],
}

const matrix: MatrixData = {
  upstream_id: 'source-a',
  refreshed: true,
  targets: config.targets,
  rows: [{
    asset: {
      id: 'source-a:key:primary',
      source_id: 'source-a',
      source_type: 'generic',
      provider: 'openai',
      raw_type: 'proxy_endpoint_key',
      kind: 'proxy_endpoint_key',
      name: 'Primary key',
      base_url: 'https://source.invalid',
      models: ['gpt-4.1', 'gpt-4o-mini'],
      enabled: true,
      secret_readable: true,
      metadata: {},
    },
    cells: [
      { target_id: 'target-a', status: 'unsynced' },
      { target_id: 'target-b', status: 'unsynced' },
    ],
  }],
}

it('previews a frozen three-step sync plan with target-specific settings', async () => {
  const fetchMock = vi.fn(async (_input: RequestInfo | URL, init: RequestInit = {}) => {
    const body = JSON.parse(String(init.body)) as { units: Array<Record<string, unknown>> }
    return new Response(JSON.stringify({
      success: true,
      data: {
        task_id: 'task-sync-plan',
        units: body.units.map((unit) => ({
          unit_id: unit.unit_id,
          asset_id: unit.asset_id,
          target_id: unit.target_id,
          status: 'synced',
        })),
      },
      request_id: 'req-plan',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
  vi.stubGlobal('fetch', fetchMock)
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useConsoleStore()
  store.config = config
  store.initialState = 'ready'
  store.matrixState = 'ready'
  store.selectedUpstreamId = 'source-a'
  const matrixWithoutValidationSummaries = structuredClone(matrix)
  matrixWithoutValidationSummaries.targets = matrixWithoutValidationSummaries.targets.map((target) => ({
    id: target.id,
    name: target.name,
    type: target.type,
    base_url: target.base_url,
    user_id: target.user_id,
  }))
  store.matrix = matrixWithoutValidationSummaries

  const user = userEvent.setup()
  render(MatrixPage, {
    global: {
      plugins: [pinia],
      stubs: {
        RouterLink: {
          template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
          props: ['to'],
        },
      },
    },
  })

  await user.click(screen.getByRole('checkbox', { name: '选择资产 Primary key' }))
  await user.click(screen.getByRole('button', { name: '批量同步 1 个资产' }))

  const dialog = screen.getByRole('dialog', { name: '批量同步设置' })
  expect(within(dialog).getByRole('navigation', { name: '同步计划步骤' })).toHaveTextContent('选择 Key / 模型')
  expect(within(dialog).getByRole('region', { name: '同步计划预览' })).toHaveTextContent('2 个同步单元')
  expect(within(dialog).getByText(/计划 revision draft-/)).toBeInTheDocument()
  expect(within(dialog).getByLabelText('Target Alpha 分组')).toBeInTheDocument()
  expect(within(dialog).getByLabelText('Target Beta 权重')).toBeInTheDocument()

  await user.clear(within(dialog).getByLabelText('分组'))
  await user.type(within(dialog).getByLabelText('分组'), 'shared')
  await user.clear(within(dialog).getByLabelText('Target Alpha 分组'))
  await user.type(within(dialog).getByLabelText('Target Alpha 分组'), 'alpha')
  await user.click(within(dialog).getByRole('button', { name: '开始同步' }))

  await waitFor(() => expect(fetchMock).toHaveBeenCalledOnce())
  expect(within(dialog).getByRole('link', { name: '查看任务详情' })).toHaveAttribute(
    'href',
    '/tasks/task-sync-plan',
  )
  const request = JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body)) as {
    units: Array<{ target_id: string; settings: { target_group: string } }>
  }
  expect(request.units.find((unit) => unit.target_id === 'target-a')?.settings.target_group).toBe('alpha')
  expect(request.units.find((unit) => unit.target_id === 'target-b')?.settings.target_group).toBe('shared')
  vi.unstubAllGlobals()
})

it('keeps unverified targets out of selection and points to target validation', async () => {
  const unverifiedConfig = structuredClone(config)
  unverifiedConfig.targets[0]!.validation_status = 'unverified'
  const matrixWithUnverified = structuredClone(matrix)
  matrixWithUnverified.targets = unverifiedConfig.targets
  matrixWithUnverified.rows[0]!.cells = [
    { target_id: 'target-a', status: 'unsynced' },
    { target_id: 'target-b', status: 'unsynced' },
  ]

  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useConsoleStore()
  store.config = unverifiedConfig
  store.initialState = 'ready'
  store.matrixState = 'ready'
  store.selectedUpstreamId = 'source-a'
  store.matrix = matrixWithUnverified

  const user = userEvent.setup()
  render(MatrixPage, {
    global: {
      plugins: [pinia],
      stubs: {
        RouterLink: {
          template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
          props: ['to'],
        },
      },
    },
  })

  const assetCheckbox = screen.getByRole('checkbox', { name: '选择资产 Primary key' })
  expect(assetCheckbox).toBeEnabled()
  expect(screen.getByText('1 个目标尚未验证')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: '前往目标实例验证' })).toHaveAttribute('href', '/targets')
  await user.click(assetCheckbox)
  await user.click(screen.getByRole('button', { name: '批量同步 1 个资产' }))

  const dialog = screen.getByRole('dialog', { name: '批量同步设置' })
  const targetCheckbox = within(dialog).getByRole('checkbox', { name: /Target Alpha/ })
  expect(targetCheckbox).toBeDisabled()
  expect(within(dialog).getByText(/请先验证目标实例/)).toBeInTheDocument()
  expect(within(dialog).getByRole('link', { name: '前往目标实例验证' })).toHaveAttribute('href', '/targets')
})

it('shows a reachable validation action when no target is eligible', () => {
  const unverifiedConfig = structuredClone(config)
  for (const target of unverifiedConfig.targets) target.validation_status = 'unverified'
  const matrixWithoutEligibleTargets = structuredClone(matrix)
  matrixWithoutEligibleTargets.targets = unverifiedConfig.targets

  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useConsoleStore()
  store.config = unverifiedConfig
  store.initialState = 'ready'
  store.matrixState = 'ready'
  store.selectedUpstreamId = 'source-a'
  store.matrix = matrixWithoutEligibleTargets

  render(MatrixPage, {
    global: {
      plugins: [pinia],
      stubs: {
        RouterLink: {
          template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
          props: ['to'],
        },
      },
    },
  })

  expect(screen.getByRole('checkbox', { name: '选择资产 Primary key' })).toBeDisabled()
  expect(screen.getByText('没有已验证的目标实例')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: '前往目标实例验证' })).toHaveAttribute('href', '/targets')
})
