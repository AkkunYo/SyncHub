/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { render, screen, within } from '@testing-library/vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { api } from '@/api/client'

import TaskDetailPage from './TaskDetailPage.vue'

const taskDetailPage = readFileSync(resolve(process.cwd(), 'src/pages/TaskDetailPage.vue'), 'utf8')

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
    expect(within(table).getAllByText('查看字段')).toHaveLength(2)
  })

  it('renders a titled empty result explanation', async () => {
    vi.spyOn(api, 'getTask').mockResolvedValue({
      ...task,
      summary: { total: 0, succeeded: 0, failed: 0 },
      items: [],
    })
    await renderPage()

    expect(await screen.findByRole('heading', { name: '暂无执行结果' })).toBeInTheDocument()
    expect(screen.getByText('任务未返回逐项结果，仍可通过上方摘要确认整体状态。')).toBeInTheDocument()
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

describe('TaskDetailPage responsive result layout', () => {
  it('keeps the desktop summary and result table compact', () => {
    const summary = declarationsFor(taskDetailPage, '.task-summary-strip')
    const table = declarationsFor(taskDetailPage, '.task-results-table')

    expect(summary).toContain('grid-template-columns: repeat(4, minmax(0, 1fr));')
    expect(table).toContain('min-width: 680px;')
  })

  it.each([320, 390, 620])('renders one divided mobile result list at %ipx', (viewportWidth) => {
    const mobileRules = blockFor(taskDetailPage, '@media (max-width: 620px)')
    const summary = declarationsFor(mobileRules, '.task-summary-strip')
    const rows = declarationsFor(mobileRules, '.task-results-table tbody tr')
    const cells = declarationsFor(mobileRules, '.task-results-table tbody td')
    const identifiers = declarationsFor(mobileRules, '.task-results-table code')
    const fields = declarationsFor(mobileRules, '.task-results-table pre')

    expect(viewportWidth).toBeLessThanOrEqual(620)
    expect(summary).toContain('grid-template-columns: minmax(0, 1fr);')
    expect(rows).toContain('border-bottom: 1px solid var(--line);')
    expect(cells).toContain('grid-template-columns: 76px minmax(0, 1fr);')
    expect(cells).toContain('min-width: 0;')
    expect(cells).toContain('overflow-wrap: anywhere;')
    expect(identifiers).toContain('white-space: normal;')
    expect(identifiers).toContain('overflow-wrap: anywhere;')
    expect(fields).toContain('position: static;')
    expect(fields).toContain('max-width: 100%;')
    expect(fields).toContain('overflow-wrap: anywhere;')
  })
})
