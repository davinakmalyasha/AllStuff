/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        bg: 'var(--color-bg)',
        surface: 'var(--color-surface)',
        surface2: 'var(--color-surface-2)',
        border: 'var(--color-border)',
        ink: 'var(--color-ink)',
        ink2: 'var(--color-ink-2)',
        ink3: 'var(--color-ink-3)',
        accent: 'var(--color-accent)',
        // Kebab-case, because `accentInk` generated `.text-accentInk` - a class that
        // appears ZERO times in the codebase, while `text-accent-ink` appears in 32
        // files and did nothing. Tailwind uses the key verbatim; it does not convert
        // camelCase to kebab-case. The effect was that every primary button inherited
        // `--color-ink` and rendered dark-on-dark: 1.12:1, invisible. Not noticed
        // because Playwright clicks by accessible name, and the accessible name is
        // unaffected by contrast.
        'accent-ink': 'var(--color-accent-ink)',
      },
      fontFamily: {
        sans: [
          'Inter',
          'system-ui',
          '-apple-system',
          'Segoe UI',
          'Roboto',
          'sans-serif',
        ],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
      boxShadow: {
        card: '0 1px 2px rgb(0 0 0 / 0.04), 0 4px 16px rgb(0 0 0 / 0.05)',
        cardHover: '0 2px 4px rgb(0 0 0 / 0.05), 0 12px 32px rgb(0 0 0 / 0.09)',
      },
      keyframes: {
        fadeUp: {
          '0%': { opacity: '0', transform: 'translateY(12px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        pulseDot: {
          '0%, 100%': { opacity: '1' },
          '50%': { opacity: '0.35' },
        },
      },
      animation: {
        fadeUp: 'fadeUp 0.5s ease-out both',
        pulseDot: 'pulseDot 1.6s ease-in-out infinite',
      },
    },
  },
  plugins: [],
}
