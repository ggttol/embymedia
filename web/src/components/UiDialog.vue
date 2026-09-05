<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { X } from 'lucide-vue-next'

defineOptions({ inheritAttrs: false })
const props = defineProps<{ title: string; busy?: boolean }>()
const emit = defineEmits<{ close: [] }>()
const titleId = useId()
const dialog = ref<HTMLDialogElement | null>(null)
let opener: HTMLElement | null = null

function requestClose() {
  if (!props.busy) emit('close')
}

function closeFromBackdrop(event: MouseEvent) {
  if (event.target !== dialog.value || !dialog.value) return
  const bounds = dialog.value.getBoundingClientRect()
  if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) requestClose()
}

function keepFocus(event: KeyboardEvent) {
  if (event.key !== 'Tab' || !dialog.value) return
  const controls = Array.from(dialog.value.querySelectorAll<HTMLElement>('a[href], button, input, select, textarea, summary, [tabindex]'))
    .filter(element => element.tabIndex >= 0 && !element.matches(':disabled') && element.getClientRects().length > 0)
  const first = controls[0]
  const last = controls[controls.length - 1]
  if (!first || !last) {
    event.preventDefault()
    dialog.value.focus()
  } else if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

onMounted(() => {
  opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
  dialog.value?.showModal()
  const field = dialog.value?.querySelector<HTMLElement>('input:not([type="hidden"]):not(:disabled), select:not(:disabled), textarea:not(:disabled), [autofocus]')
  field?.focus()
})
onBeforeUnmount(() => {
  dialog.value?.close()
  if (opener?.isConnected) opener.focus({ preventScroll: true })
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" v-bind="$attrs" class="ui-dialog" :aria-labelledby="titleId" :aria-busy="busy || undefined" @cancel.prevent="requestClose" @click="closeFromBackdrop" @keydown="keepFocus">
      <header class="ui-dialog-header">
        <h2 :id="titleId">{{ title }}</h2>
        <button type="button" data-dialog-close aria-label="关闭对话框" :disabled="busy" @click="requestClose"><X aria-hidden="true" /></button>
      </header>
      <div class="ui-dialog-content"><slot /></div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.ui-dialog {
  width: min(42rem, calc(100% - 32px));
  max-width: none;
  max-height: calc(100dvh - 32px);
  margin: auto;
  padding: 0;
  overflow: auto;
  overscroll-behavior: contain;
  border: 1px solid var(--border-strong);
  border-radius: 4px;
  background: var(--surface);
  color: var(--text);
  box-shadow: 0 16px 64px rgb(29 36 31 / 22%);
}
.ui-dialog::backdrop { background: rgb(29 36 31 / 48%); }
.ui-dialog-header {
  position: sticky;
  top: 0;
  z-index: 1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 20px;
  border-bottom: 1px solid var(--border);
  background: var(--surface);
}
.ui-dialog-header h2 { margin: 0; font: 600 22px/1.4 "Songti SC", "STSong", serif; overflow-wrap: anywhere; }
.ui-dialog-header button { display: grid; place-items: center; flex: 0 0 44px; width: 44px; height: 44px; border: 1px solid var(--border); border-radius: 4px; }
.ui-dialog-header button:disabled { opacity: .45; cursor: wait; }
.ui-dialog-header svg { width: 18px; height: 18px; }
.ui-dialog-content { padding: 20px; }
</style>
