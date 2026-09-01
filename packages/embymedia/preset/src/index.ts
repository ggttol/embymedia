import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

export const PRESET_ROOT = join(dirname(fileURLToPath(import.meta.url)), '../presets')
export const EMBY_OPERATOR_PRESET = 'emby-operator'
