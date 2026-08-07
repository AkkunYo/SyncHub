/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

const channelsPage = readFileSync(resolve(process.cwd(), 'src/pages/ChannelsPage.vue'), 'utf8')

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

describe('channel list responsive layout', () => {
  it('keeps the desktop table fields and exposes named mobile actions', () => {
    for (const heading of ['渠道', '来源', '模型', '分组', '优先级', '权重', '状态', '操作']) {
      expect(channelsPage).toContain(`<th${heading === '操作' ? ' class="actions-cell"' : ''}>${heading}</th>`)
    }
    expect(channelsPage).toContain('<span class="channel-action-label">编辑</span>')
    expect(channelsPage).toContain('<span class="channel-action-label">删除</span>')
  })

  it.each([320, 390, 620])('keeps channel filters inside the %ipx viewport', (viewportWidth) => {
    const mobileRules = blockFor(channelsPage, '@media (max-width: 620px)')
    const toolbar = declarationsFor(mobileRules, '.channels-workspace :deep(.workspace-toolbar)')
    const filters = declarationsFor(mobileRules, '.toolbar-filters')
    const search = declarationsFor(mobileRules, '.toolbar-filters .search-field')
    const refresh = declarationsFor(mobileRules, '.channel-refresh')

    expect(viewportWidth).toBeLessThanOrEqual(620)
    expect(toolbar).toContain('grid-template-columns: minmax(0, 1fr);')
    expect(filters).toContain('grid-template-columns: repeat(2, minmax(0, 1fr)) var(--touch-target);')
    expect(search).toContain('grid-column: 1 / -1;')
    expect(refresh).toContain('min-width: var(--touch-target);')
    expect(refresh).toContain('min-height: var(--touch-target);')
  })

  it('shows every mobile channel field without allowing long values to overflow', () => {
    const mobileRules = blockFor(channelsPage, '@media (max-width: 620px)')
    const row = declarationsFor(mobileRules, '.channels-table tbody tr')
    const cells = declarationsFor(mobileRules, '.channels-table td')
    const source = declarationsFor(mobileRules, '.channels-table .source-cell')
    const sourceIdentifier = declarationsFor(mobileRules, '.channels-table .source-cell small')
    const labels = declarationsFor(mobileRules, '.channels-table .channel-cell::before,')
    const actions = declarationsFor(mobileRules, '.channels-table .actions-cell')
    const actionButtons = declarationsFor(mobileRules, '.channels-table .actions-cell .icon-button')
    const modelDetails = declarationsFor(mobileRules, '.mobile-model-details p')

    expect(row).toContain('grid-template-columns: repeat(3, minmax(0, 1fr));')
    expect(cells).toContain('min-width: 0;')
    expect(cells).toContain('overflow-wrap: anywhere;')
    expect(source).toContain('grid-column: 1 / -1;')
    expect(sourceIdentifier).toContain('overflow-wrap: anywhere;')
    expect(labels).toContain('display: block;')
    expect(actions).toContain('position: static;')
    expect(actions).toContain('grid-column: 1 / -1;')
    expect(actionButtons).toContain('width: auto;')
    expect(modelDetails).toContain('overflow-wrap: anywhere;')
  })
})
