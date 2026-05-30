const { addDynamicIconSelectors } = require('@iconify/tailwind')

/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./index.html', './src/**/*.{vue,js,ts}'],
  theme: {
    extend: {
      fontFamily: {
        sans: [
          '-apple-system',
          'BlinkMacSystemFont',
          'SF Pro Display',
          'SF Pro Text',
          'Segoe UI',
          'sans-serif',
        ],
      },
      boxShadow: {
        glass: '0 30px 90px rgba(255, 255, 255, 0.10)',
        ink: '0 24px 70px rgba(0, 0, 0, 0.56)',
      },
      keyframes: {
        floatIn: {
          '0%': { opacity: '0', transform: 'translateY(18px) scale(0.98)' },
          '100%': { opacity: '1', transform: 'translateY(0) scale(1)' },
        },
        pulseRing: {
          '0%, 100%': { transform: 'scale(1)', opacity: '0.9' },
          '50%': { transform: 'scale(1.08)', opacity: '1' },
        },
      },
      animation: {
        'float-in': 'floatIn 560ms cubic-bezier(0.22, 1, 0.36, 1) both',
        'pulse-ring': 'pulseRing 2.4s ease-in-out infinite',
      },
    },
  },
  plugins: [
    addDynamicIconSelectors(),
  ],
}
