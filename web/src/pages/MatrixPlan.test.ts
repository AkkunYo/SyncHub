import { createPinia, setActivePinia } from 'pinia'
import { render, screen, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'

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
    { id: 'target-a', name: 'Target Alpha', type: 'newapi', base_url: 'https://target-a.invalid' },
    { id: 'target-b', name: 'Target Beta', type: 'cliproxyapi', base_url: 'https://target-b.invalid' },
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
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useConsoleStore()
  store.config = config
  store.initialState = 'ready'
  store.matrixState = 'ready'
  store.selectedUpstreamId = 'source-a'
  store.matrix = matrix

  const user = userEvent.setup()
  render(MatrixPage, {
    global: {
      plugins: [pinia],
      stubs: { RouterLink: { template: '<a><slot /></a>' } },
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
})
