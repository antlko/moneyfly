import js from '@eslint/js'
import globals from 'globals'
import ts from 'typescript-eslint'
import vue from 'eslint-plugin-vue'
import prettier from 'eslint-config-prettier'

export default [
  { ignores: ['dist/**', 'node_modules/**', 'src/api/schema.d.ts'] },
  js.configs.recommended,
  ...ts.configs.recommended,
  ...vue.configs['flat/recommended'],
  // Prettier owns formatting; these rules would fight it.
  prettier,
  {
    languageOptions: {
      globals: { ...globals.browser },
      parserOptions: { parser: ts.parser, ecmaVersion: 'latest', sourceType: 'module' },
    },
    rules: {
      // Single-word view names (BudgetView, PlanView) are the convention here.
      'vue/multi-word-component-names': 'off',
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      eqeqeq: ['error', 'smart'],
    },
  },
  {
    files: ['**/*.test.ts', 'vite.config.ts', 'eslint.config.js'],
    languageOptions: { globals: { ...globals.node } },
  },
]
