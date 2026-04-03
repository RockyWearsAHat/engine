import type { Config } from 'tailwindcss';

export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // Core palette — deep, refined dark
        bg: '#0c0c0e',
        surface: '#111113',
        'surface-2': '#161618',
        'surface-3': '#1c1c1f',
        border: 'rgba(255,255,255,0.07)',
        'border-strong': 'rgba(255,255,255,0.12)',
        // Text
        'text-1': '#f0f0f2',
        'text-2': '#9898a6',
        'text-3': '#55555f',
        // Accent
        accent: '#5b8def',
        'accent-dim': 'rgba(91,141,239,0.15)',
        'accent-glow': 'rgba(91,141,239,0.08)',
        // Semantic
        green: '#3dd68c',
        yellow: '#e5b80b',
        red: '#f06e6e',
        orange: '#e8855f',
        purple: '#9b72e8',
      },
      fontFamily: {
        sans: ['-apple-system', 'BlinkMacSystemFont', '"SF Pro Text"', '"Segoe UI"', 'sans-serif'],
        mono: ['"JetBrains Mono"', '"Fira Code"', 'Menlo', '"Cascadia Code"', 'monospace'],
      },
      fontSize: {
        '2xs': ['10px', '14px'],
        xs: ['11px', '16px'],
        sm: ['12px', '18px'],
        base: ['13px', '20px'],
      },
      borderRadius: {
        sm: '4px',
        md: '6px',
        lg: '8px',
        xl: '10px',
      },
      transitionDuration: {
        fast: '100ms',
        DEFAULT: '150ms',
      },
    },
  },
  plugins: [],
} satisfies Config;
