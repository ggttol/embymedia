/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{vue,js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        bg: 'var(--bg)',
        'bg-elevated': 'var(--bg-elevated)',
        'bg-muted': 'var(--bg-muted)',
        surface: 'var(--surface)',
        text: 'var(--text)',
        'text-secondary': 'var(--text-secondary)',
        'text-faint': 'var(--text-faint)',
        border: 'var(--border)',
        'border-strong': 'var(--border-strong)',
        accent: 'var(--accent)',
        'accent-strong': 'var(--accent-strong)',
        'accent-soft': 'var(--accent-soft)',
        'accent-contrast': 'var(--accent-contrast)',
        annotation: 'var(--annotation)',
        'annotation-soft': 'var(--annotation-soft)',
        danger: 'var(--danger)',
        ok: 'var(--ok)',
        warn: 'var(--warn)',
      },
      fontFamily: {
        serif: ['"Songti SC"', 'STSong', 'SimSun', 'serif'],
        sans: ['"PingFang SC"', '"Hiragino Sans GB"', '"Microsoft YaHei"', 'sans-serif'],
        mono: ['"SFMono-Regular"', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
}
