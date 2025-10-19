// eslint.config.js
// @ts-check
import eslint from '@eslint/js';
import { defineConfig } from 'eslint/config';
import tseslint from 'typescript-eslint';

export default defineConfig(
  { ignores: ['dist/**', '**/*.min.js'] },

  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    rules: {
      // '@typescript-eslint/no-unused-vars': 'off',
      // 'no-unused-vars': 'off',
    },
  },
);