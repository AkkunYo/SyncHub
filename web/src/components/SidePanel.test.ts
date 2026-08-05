import { cleanup, render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import SidePanel from './SidePanel.vue'

afterEach(() => {
  cleanup()
  document.body.replaceChildren()
})

describe('SidePanel', () => {
  it('focuses the dialog on open, closes on Escape, and restores the trigger focus', async () => {
    const user = userEvent.setup()
    const trigger = document.createElement('button')
    trigger.textContent = '打开'
    document.body.append(trigger)
    trigger.focus()
    const onClose = vi.fn()
    const view = render(SidePanel, {
      props: { title: '编辑连接', closeLabel: '关闭编辑连接', onClose },
      slots: { default: '<button type="button">保存</button>' },
    })

    const dialog = screen.getByRole('dialog', { name: '编辑连接' })
    expect(dialog).toHaveClass('side-panel')
    expect(dialog).toHaveAttribute('data-side', 'right')
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(dialog).toHaveFocus()
    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledOnce()

    view.unmount()
    expect(trigger).toHaveFocus()
  })

  it('closes from a backdrop press but stays open when pressing inside the panel', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(SidePanel, {
      props: { title: '编辑连接', onClose },
      slots: { default: '<button type="button">保存</button>' },
    })

    const dialog = screen.getByRole('dialog', { name: '编辑连接' })
    const backdrop = dialog.parentElement
    expect(backdrop).toHaveClass('side-panel-backdrop')

    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(onClose).not.toHaveBeenCalled()

    await user.click(backdrop!)
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('wraps focus between the first and last actions with Tab and Shift+Tab', async () => {
    const user = userEvent.setup()
    render(SidePanel, {
      props: { title: '编辑连接', closeLabel: '关闭编辑连接' },
      slots: {
        default: [
          '<button type="button">验证连接</button>',
          '<input aria-label="连接名称" />',
          '<button type="button">保存连接</button>',
        ].join(''),
      },
    })

    const firstAction = screen.getByRole('button', { name: '关闭编辑连接' })
    const lastAction = screen.getByRole('button', { name: '保存连接' })

    lastAction.focus()
    await user.tab()
    expect(firstAction).toHaveFocus()

    firstAction.focus()
    await user.tab({ shift: true })
    expect(lastAction).toHaveFocus()
  })
})
