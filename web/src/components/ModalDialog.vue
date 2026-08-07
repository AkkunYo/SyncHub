<script setup lang="ts">
import { onMounted, onUnmounted, ref, useId } from 'vue'
import { X } from 'lucide-vue-next'

withDefaults(defineProps<{
  title: string
  description?: string
  closeLabel?: string
  size?: 'regular' | 'wide'
}>(), {
  description: undefined,
  closeLabel: undefined,
  size: 'regular',
})

const emit = defineEmits<{
  close: []
}>()

const panel = ref<HTMLElement | null>(null)
const titleId = useId()
let previousFocus: HTMLElement | null = null
let openDialogCount = 0
let previousHtmlOverflow = ''
let previousBodyOverflow = ''

const focusableSelector = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function close(): void {
  emit('close')
}

function lockBackgroundScroll(): void {
  if (openDialogCount === 0) {
    previousHtmlOverflow = document.documentElement.style.overflow
    previousBodyOverflow = document.body.style.overflow
    document.documentElement.style.overflow = 'hidden'
    document.body.style.overflow = 'hidden'
  }
  openDialogCount += 1
}

function unlockBackgroundScroll(): void {
  if (openDialogCount === 0) return
  openDialogCount -= 1
  if (openDialogCount > 0) return
  document.documentElement.style.overflow = previousHtmlOverflow
  document.body.style.overflow = previousBodyOverflow
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    close()
    return
  }
  if (event.key !== 'Tab' || !panel.value) return
  const focusable = [...panel.value.querySelectorAll<HTMLElement>(focusableSelector)]
  if (focusable.length === 0) {
    event.preventDefault()
    panel.value.focus()
    return
  }
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  const active = document.activeElement
  if (event.shiftKey && (active === first || !panel.value.contains(active))) {
    event.preventDefault()
    last?.focus()
  } else if (!event.shiftKey && active === last) {
    event.preventDefault()
    first?.focus()
  }
}

onMounted(() => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  lockBackgroundScroll()
  document.addEventListener('keydown', onKeydown)
  panel.value?.focus()
})

onUnmounted(() => {
  document.removeEventListener('keydown', onKeydown)
  unlockBackgroundScroll()
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <Teleport to="body">
    <div class="modal-backdrop" @mousedown.self="close">
      <section
        ref="panel"
        class="modal-panel"
        :class="{ 'modal-panel-wide': size === 'wide' }"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="titleId"
        data-presentation="modal"
        tabindex="-1"
      >
        <header class="modal-header">
          <div>
            <h2 :id="titleId">{{ title }}</h2>
            <p v-if="description" class="modal-description">{{ description }}</p>
          </div>
          <button
            class="icon-button"
            type="button"
            :aria-label="closeLabel ?? '关闭对话框'"
            :title="closeLabel ?? '关闭'"
            @click="close"
          >
            <X :size="18" aria-hidden="true" />
          </button>
        </header>
        <div class="modal-body">
          <slot />
        </div>
      </section>
    </div>
  </Teleport>
</template>
