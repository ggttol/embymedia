import { ref, computed } from 'vue'

export interface FavoriteItem {
  id: number
  title: string
  url: string
  disk_type: string
  password?: string
  first_source?: string
  savedAt: string
}

const STORAGE_KEY = 'tg_favorites'
const MAX_FAVORITES = 500

function loadFromStorage(): FavoriteItem[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.filter(i => i && typeof i.id === 'number').slice(0, MAX_FAVORITES) : []
  } catch {
    return []
  }
}

const favorites = ref<FavoriteItem[]>(loadFromStorage())

function saveToStorage() {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(favorites.value))
}

export function useFavorites() {
  const count = computed(() => favorites.value.length)

  function isFavorite(id: number): boolean {
    return favorites.value.some(i => i.id === id)
  }

  function addFavorite(item: Omit<FavoriteItem, 'savedAt'>) {
    if (isFavorite(item.id)) return
    favorites.value = [
      {
        ...item,
        savedAt: new Date().toISOString()
      },
      ...favorites.value
    ].slice(0, MAX_FAVORITES)
    saveToStorage()
  }

  function removeFavorite(id: number) {
    favorites.value = favorites.value.filter(i => i.id !== id)
    saveToStorage()
  }

  function restoreFavorite(item: FavoriteItem): boolean {
    if (isFavorite(item.id)) return false
    favorites.value = [...favorites.value, item]
      .sort((a, b) => b.savedAt.localeCompare(a.savedAt))
      .slice(0, MAX_FAVORITES)
    saveToStorage()
    return true
  }

  function toggleFavorite(item: Omit<FavoriteItem, 'savedAt'>): boolean {
    if (isFavorite(item.id)) {
      removeFavorite(item.id)
      return false
    } else {
      addFavorite(item)
      return true
    }
  }

  return {
    favorites,
    count,
    isFavorite,
    addFavorite,
    removeFavorite,
    restoreFavorite,
    toggleFavorite
  }
}
