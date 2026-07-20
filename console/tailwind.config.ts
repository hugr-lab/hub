import type { Config } from 'tailwindcss'

// Colors are CSS variables defined in src/index.css (light `:root`, dark
// `[data-theme="dark"]`). Tailwind just maps semantic names onto them, so
// theming flips automatically without `dark:` variants on every element.
export default {
  darkMode: ['class', '[data-theme="dark"]'],
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        bg: 'var(--bg)',
        surface: 'var(--surface)',
        surface2: 'var(--surface2)',
        surface3: 'var(--surface3)',
        border: 'var(--border)',
        border2: 'var(--border2)',
        text: 'var(--text)',
        text2: 'var(--text2)',
        text3: 'var(--text3)',
        accent: {
          DEFAULT: 'var(--accent)',
          hi: 'var(--accent-hi)',
          soft: 'var(--accent-soft)',
          text: 'var(--accent-text)',
        },
        green: { DEFAULT: 'var(--green)', soft: 'var(--green-soft)' },
        amber: { DEFAULT: 'var(--amber)', soft: 'var(--amber-soft)' },
        red: { DEFAULT: 'var(--red)', soft: 'var(--red-soft)' },
        blue: 'var(--blue)',
      },
      fontFamily: {
        sans: ['"IBM Plex Sans"', 'system-ui', 'sans-serif'],
        mono: ['"IBM Plex Mono"', 'ui-monospace', 'monospace'],
      },
      fontSize: {
        '2xs': ['10.5px', { lineHeight: '1.4' }],
        xs: ['11.5px', { lineHeight: '1.45' }],
        sm: ['12.5px', { lineHeight: '1.45' }],
        base: ['13.5px', { lineHeight: '1.45' }],
      },
      borderRadius: {
        chip: '5px',
        btn: '8px',
        card: '11px',
        panel: '11px',
        composer: '13px',
        modal: '14px',
      },
      boxShadow: {
        card: 'var(--shadow)',
        lg: 'var(--shadow-lg)',
      },
      keyframes: {
        pulse: { '0%,100%': { opacity: '1' }, '50%': { opacity: '.35' } },
        fadeUp: {
          from: { opacity: '0', transform: 'translateY(6px)' },
          to: { opacity: '1', transform: 'translateY(0)' },
        },
        blinkc: { '0%,100%': { opacity: '1' }, '50%': { opacity: '0' } },
      },
      animation: {
        pulse: 'pulse 1.4s ease-in-out infinite',
        fadeUp: 'fadeUp .16s ease-out',
        blinkc: 'blinkc 1s step-end infinite',
      },
    },
  },
  plugins: [],
} satisfies Config
