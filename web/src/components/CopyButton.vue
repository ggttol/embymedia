<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { Check, Copy } from 'lucide-vue-next'
import UiDialog from './UiDialog.vue'

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ text: string; label?: string; compact?: boolean }>(), { label: '复制', compact: false })
const copied = ref(false)
const pending = ref(false)
const manual = ref(false)
const field = ref<HTMLTextAreaElement | null>(null)
const fieldId = useId()
let resetTimer: ReturnType<typeof setTimeout> | undefined

async function copy() {
  pending.value = true
  copied.value = false
  clearTimeout(resetTimer)
  try {
    if (!navigator.clipboard) throw new Error('Clipboard API unavailable')
    await navigator.clipboard.writeText(props.text)
    copied.value = true
    resetTimer = setTimeout(() => { copied.value = false }, 2000)
  } catch {
    // Clipboard permission and insecure origins both require a user-controlled copy.
    manual.value = true
    await nextTick()
    field.value?.focus()
    field.value?.select()
  } finally {
    pending.value = false
  }
}
watch(() => props.text, () => { copied.value = false; manual.value = false })
onBeforeUnmount(() => clearTimeout(resetTimer))
</script>

<template>
  <button v-bind="$attrs" type="button" class="copy-button" :aria-label="copied ? '已复制' : label" :title="copied ? '已复制' : label" :disabled="pending" @click="copy">
    <Check v-if="copied" aria-hidden="true" /><Copy v-else aria-hidden="true" />
    <span v-if="!compact">{{ copied ? '已复制' : pending ? '正在复制' : label }}</span>
    <span class="sr-only" role="status">{{ copied ? '已复制到剪贴板' : '' }}</span>
  </button>
  <UiDialog v-if="manual" title="手动复制" @close="manual = false">
    <p class="mb-4 text-sm text-text-muted">浏览器未允许自动复制。请复制下方已选中的文本；手机上可长按文本并选择复制。</p>
    <label :for="fieldId" class="block mb-2 text-sm font-medium">{{ label }}</label>
    <textarea :id="fieldId" ref="field" :value="text" readonly rows="5" spellcheck="false" class="copy-text" />
    <div class="mt-4 flex justify-end"><button type="button" class="copy-button" @click="manual = false">关闭</button></div>
  </UiDialog>
</template>

<style scoped>
.copy-button { display: inline-flex; min-height: 44px; min-width: 44px; align-items: center; justify-content: center; gap: 6px; padding: 8px 12px; border: 1px solid var(--border); border-radius: 4px; background: var(--surface); color: var(--text); font-size: 13px; }
.copy-button:hover { border-color: var(--accent); color: var(--accent); }
.copy-button:disabled { opacity: .6; cursor: wait; }
.copy-button svg { width: 16px; height: 16px; flex-shrink: 0; }
.copy-text { width: 100%; padding: 12px; border: 1px solid var(--border-strong); border-radius: 4px; background: var(--bg-elevated); color: var(--text); font: 13px/1.65 "SFMono-Regular", Consolas, monospace; overflow-wrap: anywhere; }
</style>
