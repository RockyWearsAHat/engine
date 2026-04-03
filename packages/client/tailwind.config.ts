import type { Config } from 'tailwindcss';

export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        editor: {
          bg: '#0d0d0d',
          surface: '#141414',
          border: '#222222',
          hover: '#1a1a1a',
          active: '#1e1e2e',
        },
      },
      fontFamily: {
        mono: ['JetBrains Mono', 'Fira Code', 'Cascadia Code', 'Menlo', 'monospace'],
      },
    },
  },
  plugins: [],
} satisfies Config;
