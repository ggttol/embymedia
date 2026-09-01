import { defineConfig } from 'tsdown'

export default defineConfig({
  name: '@embymedia/dsh-preset',
  entry: { index: 'lib/types/index.js' },
  outDir: 'lib',
  format: ['esm'],
  platform: 'node',
  target: 'es2024',
  fixedExtension: false,
  dts: false,
  clean: false,
})
