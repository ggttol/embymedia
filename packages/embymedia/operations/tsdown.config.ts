import { defineConfig } from 'tsdown'

export default defineConfig({
  name: '@embymedia/dsh-operations',
  entry: {
    index: 'lib/types/index.js',
    invariant: 'lib/types/invariant.js',
    tools: 'lib/types/tools/index.js',
  },
  outDir: 'lib',
  format: ['esm'],
  platform: 'node',
  target: 'es2024',
  fixedExtension: false,
  dts: false,
  clean: false,
})
