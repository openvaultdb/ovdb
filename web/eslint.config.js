// Lint for the console and apps. Its one job today is
// local-server-and-web-console#REQ:security-headers and
// database-context-navigation#REQ:browse-data-read-only: record values and
// copy are rendered as text, never as HTML (vue/no-v-html), alongside
// tests/text-only.test.ts, which also catches innerHTML in scripts.
import pluginVue from 'eslint-plugin-vue'
import tseslint from 'typescript-eslint'

export default [
  { ignores: ['dist/**', 'node_modules/**', 'test-results/**'] },
  ...pluginVue.configs['flat/essential'],
  {
    files: ['**/*.vue', '**/*.ts'],
    languageOptions: {
      parserOptions: { parser: tseslint.parser, ecmaVersion: 'latest', sourceType: 'module' },
    },
  },
  {
    files: ['**/*.ts'],
    languageOptions: { parser: tseslint.parser },
  },
  {
    rules: {
      'vue/no-v-html': 'error',
      'vue/multi-word-component-names': 'off',
    },
  },
]
