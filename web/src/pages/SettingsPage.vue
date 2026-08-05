<script setup lang="ts">
import { reactive, ref, watchEffect } from 'vue'
import { Save } from 'lucide-vue-next'

import { api, safeErrorMessage } from '@/api/client'
import { useConsoleStore } from '@/stores/console'
import type { AppSettings } from '@/types'

const store = useConsoleStore()
const notice = ref('')
const error = ref('')
const saving = ref(false)
const form = reactive<AppSettings>({
  host: '127.0.0.1',
  port: 8888,
  reconcile_interval: '5m0s',
  request_timeout: '15s',
  sync_concurrency: 4,
})

watchEffect(() => {
  if (!store.config) return
  Object.assign(form, store.config.app)
})

async function saveRuntimeSettings(): Promise<void> {
  if (!form.host.trim() || form.port < 1 || form.port > 65535) {
    error.value = '监听地址或端口无效'
    return
  }
  if (form.sync_concurrency < 1) {
    error.value = '同步并发数至少为 1'
    return
  }

  saving.value = true
  error.value = ''
  notice.value = ''
  try {
    const settings = await api.updateApp({ ...form, host: form.host.trim() })
    store.replaceAppSettings(settings)
    notice.value = '运行设置已保存'
  } catch (reason) {
    error.value = safeErrorMessage(reason)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="page" aria-labelledby="settings-heading">
    <header class="page-header">
      <div>
        <h1 id="settings-heading" aria-label="设置">系统设置</h1>
      </div>
    </header>

    <section class="settings-band settings-surface" aria-labelledby="runtime-settings-heading">
      <header class="section-header">
        <h2 id="runtime-settings-heading">运行参数</h2>
      </header>

      <form class="runtime-form" @submit.prevent="saveRuntimeSettings">
        <label class="field">
          <span>监听地址</span>
          <input v-model="form.host" type="text" autocomplete="off" />
        </label>
        <label class="field">
          <span>端口</span>
          <input v-model.number="form.port" type="number" min="1" max="65535" />
        </label>
        <label class="field">
          <span>校验间隔</span>
          <input v-model="form.reconcile_interval" type="text" autocomplete="off" />
        </label>
        <label class="field">
          <span>请求超时</span>
          <input v-model="form.request_timeout" type="text" autocomplete="off" />
        </label>
        <label class="field">
          <span>同步并发</span>
          <input v-model.number="form.sync_concurrency" type="number" min="1" />
        </label>

        <div class="runtime-actions">
          <button class="primary-button" type="submit" :disabled="saving">
            <Save :size="16" aria-hidden="true" />
            {{ saving ? '保存中' : '保存运行设置' }}
          </button>
        </div>
      </form>

      <p v-if="notice" class="notice notice-success" role="status">{{ notice }}</p>
      <p v-if="error" class="notice notice-error" role="alert">{{ error }}</p>
    </section>
  </section>
</template>

<style scoped>
.settings-band {
  margin: 0;
  padding: 0 12px 14px;
  border: 0;
  border-radius: 0;
  box-shadow: none;
}

.settings-surface {
  min-width: 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
  background: var(--surface);
}

.section-header {
  min-height: 50px;
}

.section-header h2 {
  margin: 0;
  font-size: 14px;
}

.runtime-form {
  display: grid;
  padding-top: 10px;
  gap: 14px 18px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.runtime-actions {
  display: flex;
  align-items: flex-end;
  justify-content: flex-end;
}

@media (max-width: 620px) {
  .settings-band {
    padding-right: 8px;
    padding-left: 8px;
  }

  .runtime-form {
    grid-template-columns: 1fr;
  }

  .runtime-actions .primary-button {
    width: 100%;
  }
}
</style>
