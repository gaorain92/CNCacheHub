/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{vue,ts,tsx,js}'],
  theme: {
    extend: {
      colors: {
        ink: '#020617',
        panel: '#0f172a',
        panel2: '#111827',
        line: '#1f2937',
        mint: '#2dd4bf',
        violet: '#8b5cf6',
        lime: '#a3e635',
      },
      fontFamily: {
        sans: [
          '-apple-system',
          'BlinkMacSystemFont',
          '"SF Pro Text"',
          '"Segoe UI"',
          'Roboto',
          '"Helvetica Neue"',
          'Arial',
          '"PingFang SC"',
          '"Microsoft YaHei"',
          'sans-serif',
        ],
      },
      boxShadow: {
        glow: '0 0 40px rgba(45,212,191,.22)',
        card: '0 24px 80px rgba(0,0,0,.36)',
      },
      keyframes: {
        pulse: {
          '0%,100%': { opacity: '1' },
          '50%': { opacity: '.38' },
        },
        rise: {
          from: { opacity: '0', transform: 'translateY(12px)' },
          to: { opacity: '1', transform: 'translateY(0)' },
        },
      },
      animation: {
        pulse: 'pulse 1.8s ease infinite',
        rise: 'rise .32s ease both',
      },
    },
  },
  plugins: [],
  corePlugins: {
    preflight: true,
  },
}
