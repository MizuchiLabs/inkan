import { plugin as shadcn } from '@shadcn/lint';
import tsParser from '@typescript-eslint/parser';
import { defineConfig } from 'eslint/config';
import svelteParser from 'svelte-eslint-parser';

export default defineConfig([
	{
		files: ['**/*.svelte'],
		languageOptions: {
			parser: svelteParser,
			parserOptions: { parser: tsParser }
		},
		plugins: { shadcn },
		rules: {
			'shadcn/no-restyle': ['error', { allow: ['layout'] }],
			'shadcn/no-arbitrary-values': [
				'error',
				{ allow: ['grid-cols-[auto_1fr]', 'min-h-[calc(100dvh-12rem)]'] }
			]
		}
	},
	{
		ignores: ['src/lib/components/ui/**']
	}
]);
