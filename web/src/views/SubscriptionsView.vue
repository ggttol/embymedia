<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Plus, RefreshCw } from 'lucide-vue-next'
import UiDialog from '@/components/UiDialog.vue'

interface ShareSubscription {
  id: string
  name: string
  provider: string
  url: string
  password?: string
  target_cid: string
  active: boolean
  last_cursor_time?: string
  last_error?: string
  last_sync_at?: string
  created_at: string
  updated_at: string
}

const loading = ref(true)
const subscriptions = ref<ShareSubscription[]>([])
const error = ref('')
const selectedSub = ref<ShareSubscription | null>(null)
const editMode = ref(false)

const form = ref<Partial<ShareSubscription>>({
  active: true,
  provider: 'quark',
})

async function fetchSubscriptions() {
  loading.value = true
  error.value = ''
  try {
    const response = await fetch('/api/v1/share-subscriptions')
    if (!response.ok) {
      throw new Error(`请求失败 (HTTP ${response.status})`)
    }
    const data = await response.json()
    subscriptions.value = data.share_subscriptions || []
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

async function saveSubscription() {
  try {
    const url = editMode.value
      ? `/api/v1/share-subscriptions/${selectedSub.value?.id}`
      : '/api/v1/share-subscriptions'
    const response = await fetch(url, {
      method: editMode.value ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(form.value)
    })

    if (!response.ok) {
      const errData = await response.json().catch(() => ({}))
      throw new Error(errData.error || `保存失败 (HTTP ${response.status})`)
    }

    selectedSub.value = null
    await fetchSubscriptions()
  } catch (err) {
    alert(err instanceof Error ? err.message : String(err))
  }
}

async function toggleActive(sub: ShareSubscription) {
  try {
    const response = await fetch(`/api/v1/share-subscriptions/${sub.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ active: !sub.active })
    })
    if (!response.ok) throw new Error(`状态切换失败`)
    await fetchSubscriptions()
  } catch (err) {
    alert(err instanceof Error ? err.message : String(err))
  }
}

function openEdit(sub?: ShareSubscription) {
  editMode.value = !!sub
  if (sub) {
    form.value = { ...sub }
  } else {
    form.value = { provider: 'quark', active: true, target_cid: '0' }
  }
  selectedSub.value = sub || { id: 'new' } as ShareSubscription
}

onMounted(() => {
  void fetchSubscriptions()
})
</script>

<template>
  <div class="px-6 py-8 mx-auto max-w-7xl">
    <header class="flex items-center justify-between mb-8">
      <div>
        <h1 class="text-2xl font-bold tracking-tight text-gray-900">分享订阅</h1>
        <p class="text-sm text-gray-500 mt-1">自动追踪网盘分享链接更新，直接在云端保存增量连载资源。</p>
      </div>
      <button
        @click="openEdit()"
        class="inline-flex items-center justify-center rounded-md text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-gray-950 disabled:pointer-events-none disabled:opacity-50 bg-gray-900 text-gray-50 shadow hover:bg-gray-900/90 h-9 px-4 py-2"
      >
        <Plus aria-hidden="true" class="w-4 h-4 mr-2" />
        添加订阅
      </button>
    </header>

    <div v-if="error" class="bg-red-50 text-red-600 p-4 rounded-md mb-6 whitespace-pre-wrap">
      {{ error }}
    </div>

    <div v-if="loading && !subscriptions.length" class="flex justify-center p-12">
      <RefreshCw class="w-8 h-8 text-gray-400 animate-spin" />
    </div>

    <div v-else-if="!subscriptions.length" class="border-2 border-dashed border-gray-200 rounded-lg p-12 text-center">
      <div class="text-gray-500 mb-4">暂无分享订阅配置</div>
    </div>

    <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
      <div
        v-for="sub in subscriptions"
        :key="sub.id"
        class="border rounded-lg bg-white overflow-hidden shadow-sm flex flex-col"
        :class="sub.active ? 'border-gray-200' : 'border-gray-200 opacity-60'"
      >
        <div class="p-5 flex-1 line-clamp-1">
          <div class="flex items-center justify-between mb-2">
            <span
              class="inline-flex items-center rounded-sm px-2 py-0.5 text-xs font-medium"
              :class="sub.provider === 'quark' ? 'bg-indigo-50 text-indigo-700' : 'bg-blue-50 text-blue-700'"
            >
              {{ sub.provider.toUpperCase() }}
            </span>
            <button
              @click="toggleActive(sub)"
              class="text-xs font-medium px-2 py-1 rounded"
              :class="sub.active ? 'text-green-600 bg-green-50 hover:bg-green-100' : 'text-gray-500 bg-gray-100 hover:bg-gray-200'"
            >
              {{ sub.active ? '监听中' : '未启用' }}
            </button>
          </div>
          <h3 class="font-medium text-gray-900 truncate mb-1" :title="sub.name">{{ sub.name }}</h3>
          <div class="text-xs text-gray-500 truncate" :title="sub.url">
            {{ sub.url }}
          </div>
          <div v-if="sub.last_error" class="text-xs text-red-500 mt-2 truncate" :title="sub.last_error">
            异常: {{ sub.last_error }}
          </div>
        </div>
        <div class="bg-gray-50 px-5 py-3 border-t border-gray-100 flex items-center justify-between text-xs text-gray-500">
          <span class="truncate" :title="sub.last_sync_at ? new Date(sub.last_sync_at).toLocaleString() : '从未同步'">
            上次流转: {{ sub.last_sync_at ? new Date(sub.last_sync_at).toLocaleString() : '-' }}
          </span>
          <button
            @click="openEdit(sub)"
            class="font-medium hover:text-gray-900 ml-4 flex-shrink-0"
          >
            配置
          </button>
        </div>
      </div>
    </div>

    <!-- Edit Dialog -->
    <UiDialog
      v-if="selectedSub"
      :title="editMode ? '编辑自动转存订阅' : '添加网盘分享订阅'"
      @close="selectedSub = null"
    >
      <form @submit.prevent="saveSubscription" class="space-y-4 pt-4">
        <div>
          <label class="block text-sm font-medium leading-6 text-gray-900">任务名称名称</label>
          <div class="mt-2">
            <input
              v-model="form.name"
              type="text"
              required
              placeholder="例如：交锋 夸克分享源"
              class="block w-full rounded-md border-0 py-1.5 text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 placeholder:text-gray-400 focus:ring-2 focus:ring-inset focus:ring-indigo-600 sm:text-sm sm:leading-6"
            />
          </div>
        </div>

        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium leading-6 text-gray-900">来源盘类型</label>
            <div class="mt-2">
              <select
                v-model="form.provider"
                class="block w-full rounded-md border-0 py-1.5 text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 focus:ring-2 focus:ring-inset focus:ring-indigo-600 sm:text-sm sm:leading-6"
              >
                <option value="quark">夸克网盘</option>
                <option value="115">115</option>
              </select>
            </div>
          </div>
          <div>
            <label class="block text-sm font-medium leading-6 text-gray-900">目标 115 CID</label>
            <div class="mt-2">
              <input
                v-model="form.target_cid"
                type="text"
                required
                class="block w-full rounded-md border-0 py-1.5 text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 focus:ring-2 focus:ring-inset focus:ring-indigo-600 sm:text-sm sm:leading-6"
              />
            </div>
          </div>
        </div>

        <div>
           <label class="block text-sm font-medium leading-6 text-gray-900">分享链接 URL</label>
            <div class="mt-2">
              <input
                v-model="form.url"
                type="text"
                required
                class="block w-full rounded-md border-0 py-1.5 text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 focus:ring-2 focus:ring-inset focus:ring-indigo-600 sm:text-sm sm:leading-6"
              />
            </div>
        </div>

        <div>
           <label class="block text-sm font-medium leading-6 text-gray-900">分享提取码（可选）</label>
            <div class="mt-2">
              <input
                v-model="form.password"
                type="text"
                class="block w-full rounded-md border-0 py-1.5 text-gray-900 shadow-sm ring-1 ring-inset ring-gray-300 focus:ring-2 focus:ring-inset focus:ring-indigo-600 sm:text-sm sm:leading-6"
              />
            </div>
        </div>

        <div>
           <label class="flex items-center space-x-2 text-sm font-medium leading-6 text-gray-900 cursor-pointer">
             <input type="checkbox" v-model="form.active" class="rounded border-gray-300 text-indigo-600 focus:ring-indigo-600" />
             <span>立刻启用监控</span>
            </label>
        </div>

        <div class="pt-4 flex items-center justify-end gap-x-3">
          <button
            type="button"
            @click="selectedSub = null"
            class="text-sm font-semibold leading-6 text-gray-900 px-3 py-2 hover:bg-gray-50 rounded"
          >
            取消
          </button>
          <button
            type="submit"
            class="rounded-md bg-indigo-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-indigo-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-600"
          >
            {{ editMode ? '保存修改' : '创建订阅' }}
          </button>
        </div>
      </form>
    </UiDialog>
  </div>
</template>
