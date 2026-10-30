import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

// Mirrors apps/web/eslint.config.js: same rule set, so the migrated tree is
// held to the same gate. Differences are mechanical: `dist` -> `.next`,
// no vite.config.ts, plus scripts/*.mjs (node) and proxy.ts (edge runtime).
export default tseslint.config(
  { ignores: ['node_modules', '.next', 'playwright-report', 'test-results', 'coverage', 'public/maplibre-worker.mjs'] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-refresh/only-export-components': 'off',
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
    },
  },
  {
    files: ['scripts/**/*.mjs', 'proxy.ts'],
    languageOptions: {
      globals: { ...globals.node },
    },
  },
)
