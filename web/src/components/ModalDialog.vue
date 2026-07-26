<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { X } from 'lucide-vue-next'

defineProps<{
  title: string
  description?: string
  closeLabel?: string
}>()

const emit = defineEmits<{
  close: []
}>()

const panel = ref<HTMLElement | null>(null)
let previousFocus: HTMLElement | null = null

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
  document.addEventListener('keydown', onKeydown)
  panel.value?.focus()
})

onUnmounted(() => {
  document.removeEventListener('keydown', onKeydown)
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <Teleport to="body">
    <div class="modal-backdrop" @mousedown.self="close">
      <section
        ref="panel"
        class="modal-panel"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="`${title}-title`"
        tabindex="-1"
      >
        <header class="modal-header">
          <div>
            <h2 :id="`${title}-title`">{{ title }}</h2>
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
