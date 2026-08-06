import { render, screen, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import TasksPage, { type TaskRecord } from './TasksPage.vue'

const routerLinkStub = {
  props: ['to'],
  template: '<a :href="typeof to === \'string\' ? to : `/tasks/${to.params.id}`"><slot /></a>',
}

describe('TasksPage workflow states', () => {
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

    expect(screen.getByText('暂无任务记录')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '返回同步工作台' })).toHaveAttribute('href', '/sync')
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
})
