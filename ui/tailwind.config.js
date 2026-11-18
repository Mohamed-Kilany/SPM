/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        navy: {
          50: '#f0f4ff',
          100: '#d9e4ff',
          200: '#b3c9ff',
          300: '#8dadff',
          400: '#6692ff',
          500: '#4070f4',
          600: '#2a50c8',
          700: '#1e3a96',
          800: '#152868',
          900: '#0f172a',
          950: '#070e1a',
        },
        accent: {
          50: '#f5f3ff',
          100: '#ede9fe',
          200: '#ddd6fe',
          300: '#c4b5fd',
          400: '#a78bfa',
          500: '#8b5cf6',
          600: '#7c3aed',
          700: '#6d28d9',
          800: '#5b21b6',
          900: '#4c1d95',
          950: '#2e1065',
        },
      },
      fontFamily: {
        sans: ['ui-sans-serif', 'system-ui', '-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'sans-serif'],
      },
      backdropBlur: {
        glass: '16px',
      },
      boxShadow: {
        glow: '0 16px 44px -20px rgba(124, 58, 237, 0.65)',
        glass: '0 24px 70px -32px rgba(0, 0, 0, 0.9)',
      },
      keyframes: {
        'skeleton-pulse': {
          '0%, 100%': { backgroundColor: '#152868' },
          '50%': { backgroundColor: '#1e3a96' },
        },
        'fade-scale-in': {
          '0%': { opacity: '0', transform: 'scale(0.95)' },
          '100%': { opacity: '1', transform: 'scale(1)' },
        },
        'slide-up': {
          '0%': { opacity: '0', transform: 'translateY(16px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        'fade-in': {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        'drawer-in': {
          '0%': { opacity: '0', transform: 'translateX(24px)' },
          '100%': { opacity: '1', transform: 'translateX(0)' },
        },
      },
      animation: {
        'skeleton-pulse': 'skeleton-pulse 1.5s ease-in-out infinite',
        'fade-scale-in': 'fade-scale-in 0.6s ease-out both',
        'slide-up': 'slide-up 0.3s ease-out both',
        'fade-in': 'fade-in 0.2s ease-out both',
        'drawer-in': 'drawer-in 0.26s cubic-bezier(0.22, 1, 0.36, 1) both',
      },
    },
  },
  plugins: [],
}
