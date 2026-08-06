import { render, screen, within } from '@testing-library/vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { api } from '@/api/client'

import TaskDetailPage from './TaskDetailPage.vue'

const task = {
  task_id: 'task-detail',
  type: 'sync',
  scope: 'source-a -> target-a',
  status: 'partially_failed' as const,
  completed: true,
  started_at: '2026-08-06T14:20:00Z',
  completed_at: '2026-08-06T14:20:04Z',
  summary: { total: 2, succeeded: 1, failed: 1 },
  items: [
    { item_id: 'asset-1', status: 'succeeded', target_id: 'target-a' },
    { item_id: 'asset-2', status: 'failed', target_id: 'target-b', error_code: 'timeout' },
  ],
}

async function renderPage() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/tasks/:id', name: 'task-detail', component: TaskDetailPage },
      { path: '/tasks', name: 'tasks', component: { template: '<div />' } },
    ],
  })
  await router.push('/tasks/task-detail')
  await router.isReady()
  return { ...render(TaskDetailPage, { global: { plugins: [router] } }), router }
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('TaskDetailPage', () => {
  it('loads and renders task summary and sanitized item results', async () => {
    vi.spyOn(api, 'getTask').mockResolvedValue(task)
    await renderPage()

    expect(await screen.findByRole('heading', { name: '任务详情' })).toBeInTheDocument()
    expect(screen.getByText('task-detail')).toBeInTheDocument()
    expect(screen.getByText('部分失败')).toBeInTheDocument()
    expect(screen.getByText('2 个结果')).toBeInTheDocument()
    const table = screen.getByRole('table', { name: '任务结果' })
    expect(within(table).getByText('asset-2')).toBeInTheDocument()
    expect(within(table).getByText('timeout')).toBeInTheDocument()
  })

  it('renders a retryable missing-task error', async () => {
    const getTask = vi.spyOn(api, 'getTask').mockRejectedValue(new Error('任务不存在'))
    const user = (await import('@testing-library/user-event')).default.setup()
    await renderPage()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('任务详情加载失败')
    await user.click(within(alert).getByRole('button', { name: '重试任务详情' }))
    expect(getTask).toHaveBeenCalledTimes(2)
  })
})
