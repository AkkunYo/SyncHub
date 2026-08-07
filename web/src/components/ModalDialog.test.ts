import { fireEvent, render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import ModalDialog from './ModalDialog.vue'

describe('ModalDialog', () => {
  it('provides a default close command and supports backdrop and keyboard dismissal', async () => {
    const view = render(ModalDialog, {
      props: { title: '确认操作', description: '请核对当前设置' },
      slots: { default: '<p>对话框内容</p>' },
    })

    expect(screen.getByText('请核对当前设置')).toBeInTheDocument()
    await fireEvent.keyDown(document, { key: 'Enter' })
    expect(view.emitted('close')).toBeUndefined()
    await fireEvent.keyDown(document, { key: 'Escape' })
    expect(view.emitted('close')).toHaveLength(1)

    await fireEvent.click(screen.getByRole('button', { name: '关闭对话框' }))
    expect(view.emitted('close')).toHaveLength(2)
    const backdrop = screen.getByRole('dialog').parentElement
    expect(backdrop).not.toBeNull()
    await fireEvent.mouseDown(backdrop as HTMLElement)
    expect(view.emitted('close')).toHaveLength(3)
  })

  it('traps forward and reverse tab focus and restores the trigger on close', async () => {
    const trigger = document.createElement('button')
    trigger.textContent = '打开设置'
    document.body.append(trigger)
    trigger.focus()
    const view = render(ModalDialog, {
      props: { title: '键盘设置' },
      slots: {
        default: '<button type="button">第一个操作</button><input aria-label="中间字段"><button type="button">最后一个操作</button>',
      },
    })

    const first = screen.getByRole('button', { name: '关闭对话框' })
    const last = screen.getByRole('button', { name: '最后一个操作' })
    last.focus()
    await fireEvent.keyDown(document, { key: 'Tab' })
    expect(first).toHaveFocus()

    first.focus()
    await fireEvent.keyDown(document, { key: 'Tab', shiftKey: true })
    expect(last).toHaveFocus()

    await fireEvent.keyDown(document, { key: 'Escape' })
    view.unmount()
    expect(trigger).toHaveFocus()
    trigger.remove()
  })

  it('keeps background scrolling locked until the last nested dialog closes', () => {
    const html = document.documentElement
    const previousHtmlOverflow = html.style.overflow
    const previousBodyOverflow = document.body.style.overflow
    html.style.overflow = 'clip'
    document.body.style.overflow = 'scroll'

    let outer: ReturnType<typeof render> | undefined
    let inner: ReturnType<typeof render> | undefined
    try {
      outer = render(ModalDialog, { props: { title: '外层对话框' } })
      expect(html.style.overflow).toBe('hidden')
      expect(document.body.style.overflow).toBe('hidden')

      inner = render(ModalDialog, { props: { title: '内层对话框' } })
      expect(html.style.overflow).toBe('hidden')
      expect(document.body.style.overflow).toBe('hidden')

      inner.unmount()
      inner = undefined
      expect(html.style.overflow).toBe('hidden')
      expect(document.body.style.overflow).toBe('hidden')

      outer.unmount()
      outer = undefined
      expect(html.style.overflow).toBe('clip')
      expect(document.body.style.overflow).toBe('scroll')
    } finally {
      inner?.unmount()
      outer?.unmount()
      html.style.overflow = previousHtmlOverflow
      document.body.style.overflow = previousBodyOverflow
    }
  })
})
