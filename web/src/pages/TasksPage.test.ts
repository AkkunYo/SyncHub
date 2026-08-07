import { render, screen, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import TasksPage, { type TaskRecord } from './TasksPage.vue'
import { api } from '@/api/client'

const routerLinkStub = {
  props: ['to'],
  template: '<a :href="typeof to === \'string\' ? to : `/tasks/${to.params.id}`"><slot /></a>',
}

describe('TasksPage workflow states', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('loads task history automatically and links rows to task details', async () => {
    vi.spyOn(api, 'getTasks').mockResolvedValue({
      tasks: [{
        task_id: 'task-auto',
        type: 'sync',
        scope: 'source-a -> target-a',
        status: 'succeeded',
        completed: true,
        started_at: '2026-08-06T14:20:00Z',
        completed_at: '2026-08-06T14:20:04Z',
        summary: { total: 1, succeeded: 1, failed: 0 },
      }],
      meta: { total: 1, capacity: 50 },
    })

    render(TasksPage, { global: { stubs: { RouterLink: routerLinkStub } } })

    const table = await screen.findByRole('table', { name: '任务状态列表' })
    expect(within(table).getByText('资产同步')).toBeInTheDocument()
    expect(within(table).getByText('task-auto')).toBeInTheDocument()
    expect(within(table).getByRole('link', { name: '资产同步' })).toHaveAttribute('href', '/tasks/task-auto')
  })

  it('shows an automatic load error and retries the request', async () => {
    const getTasks = vi.spyOn(api, 'getTasks')
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ tasks: [], meta: { total: 0, capacity: 50 } })
    const user = userEvent.setup()

    render(TasksPage, { global: { stubs: { RouterLink: routerLinkStub } } })

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('任务记录加载失败')
    await user.click(within(alert).getByRole('button', { name: '重试任务记录' }))
    await screen.findByText('暂无任务记录')
    expect(getTasks).toHaveBeenCalledTimes(2)
  })

  it('renders the loading state without exposing an empty table', () => {
    render(TasksPage, { props: { loading: true } })

    expect(screen.getByRole('status', { name: '正在加载任务记录' })).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('exposes a retry action when loading task history fails', async () => {
    const onRetry = vi.fn()
    const user = userEvent.setup()
    render(TasksPage, {
      props: { error: '任务服务暂时不可用', onRetry },
    })

    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('任务记录加载失败')
    expect(alert).toHaveTextContent('任务服务暂时不可用')
    await user.click(within(alert).getByRole('button', { name: '重试任务记录' }))
    expect(onRetry).toHaveBeenCalledOnce()
  })

  it('keeps the empty state actionable when history has no records', () => {
    render(TasksPage, { props: { tasks: [] }, global: { stubs: { RouterLink: routerLinkStub } } })

    const emptyState = screen.getByRole('status', { name: '暂无任务记录' })
    expect(emptyState).toHaveTextContent('同步或校验任务执行后，会在这里保留状态与时间记录。')
    const workspaceLink = within(emptyState).getByRole('link', { name: '返回同步工作台' })
    expect(workspaceLink).toHaveTextContent('前往同步工作台')
    expect(workspaceLink).toHaveAttribute('href', '/sync')
    expect(screen.queryByRole('table', { name: '任务状态列表' })).not.toBeInTheDocument()
  })

  it('renders task rows with status, scope, and detail links', () => {
    const task: TaskRecord = {
      id: 'sync-42',
      type: 'sync',
      scope: 'source-a -> target-a',
      status: 'partially_failed',
      startedAt: '2026-08-06 14:20:00',
      detail: '1 个目标需要重试',
    }
    render(TasksPage, {
      props: { tasks: [task] },
      global: { stubs: { RouterLink: routerLinkStub } },
    })

    const row = screen.getByRole('row', { name: /资产同步 source-a -> target-a/ })
    expect(within(row).getByText('sync-42')).toBeInTheDocument()
    expect(within(row).getByText('部分失败')).toBeInTheDocument()
    expect(within(row).getByText('1 个目标需要重试')).toBeInTheDocument()
    expect(within(row).getByRole('link', { name: '资产同步' })).toHaveAttribute('href', '/tasks/sync-42')
    expect(within(row).getByText('2026-08-06 14:20:00')).toBeInTheDocument()
    expect(screen.queryByText('暂无任务记录')).not.toBeInTheDocument()
  })

  it('provides a semantic mobile task list and keeps long identifiers wrap-safe', () => {
    const task: TaskRecord = {
      id: 'sync-20260807-source-with-a-very-long-identifier-target-with-a-very-long-identifier',
      type: 'sync',
      scope: 'source-a -> target-a',
      status: 'running',
      startedAt: '2026-08-07 09:30:00',
    }
    render(TasksPage, {
      props: { tasks: [task] },
      global: { stubs: { RouterLink: routerLinkStub } },
    })

    const mobileList = screen.getByRole('list', { name: '移动端任务状态列表' })
    const item = within(mobileList).getByRole('listitem', { name: /资产同步 source-a -> target-a/ })
    expect(within(item).getByText('任务类型')).toBeInTheDocument()
    expect(within(item).getByText('范围')).toBeInTheDocument()
    expect(within(item).getByText('状态')).toBeInTheDocument()
    expect(within(item).getByText('开始时间')).toBeInTheDocument()
    expect(within(item).getByText('完成时间')).toBeInTheDocument()
    expect(within(item).getByText(task.id)).toHaveClass('task-id')
    expect(within(item).getByText('--')).toBeInTheDocument()
  })
})
