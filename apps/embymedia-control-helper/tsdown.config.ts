import { defineConfig } from 'tsdown'

export default defineConfig({
  name: '@embymedia/control-helper',
  entry: { index: 'lib/types/index.js', bin: 'lib/types/bin.js' },
  outDir: 'lib',
  format: ['esm'],
  platform: 'node',
  target: 'es2024',
  fixedExtension: false,
  dts: false,
  clean: false,
})
