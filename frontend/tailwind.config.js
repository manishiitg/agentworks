import plugin from 'tailwindcss/plugin'
import tailwindcssAnimate from "tailwindcss-animate"

/** @type {import('tailwindcss').Config} */
export default {
  // Same as 'class' (a descendant of .dark), except inside a .force-light surface,
  // so a dashboard can ask for a light chat while the app itself is dark. The
  // :where() keeps the specificity identical to the plain class strategy.
  darkMode: ['variant', '&:is(.dark *):not(:where(.force-light *))'],
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      // App text scales without changing rem units inside authored documents.
      fontSize: Object.fromEntries(Object.entries({ xs: [12,16], sm: [14,20], base: [16,24], lg: [18,28], xl: [20,28], '2xl': [24,32], '3xl': [30,36], '4xl': [36,40], '5xl': [48,48], '6xl': [60,60] }).map(([name, [size, line]]) => [name, [`calc(var(--app-font-size, 14px) * ${size / 14})`, { lineHeight: `calc(var(--app-font-size, 14px) * ${line / 14})` }]])),
      fontFamily: {
        sans: ['var(--app-font, system-ui)', 'sans-serif'],
        mono: ['var(--app-code-font, monospace)', 'monospace'],
      },
      borderRadius: {
        lg: 'var(--radius)',
        md: 'calc(var(--radius) - 2px)',
        sm: 'calc(var(--radius) - 4px)'
      },
      colors: {
        overlay: 'hsl(var(--overlay) / <alpha-value>)',
        background: 'hsl(var(--background) / <alpha-value>)',
        foreground: 'hsl(var(--foreground) / <alpha-value>)',
        card: {
          DEFAULT: 'hsl(var(--card) / <alpha-value>)',
          foreground: 'hsl(var(--card-foreground) / <alpha-value>)'
        },
        popover: {
          DEFAULT: 'hsl(var(--popover) / <alpha-value>)',
          foreground: 'hsl(var(--popover-foreground) / <alpha-value>)'
        },
        primary: {
          DEFAULT: 'hsl(var(--primary) / <alpha-value>)',
          foreground: 'hsl(var(--primary-foreground) / <alpha-value>)'
        },
        secondary: {
          DEFAULT: 'hsl(var(--secondary) / <alpha-value>)',
          foreground: 'hsl(var(--secondary-foreground) / <alpha-value>)'
        },
        muted: {
          DEFAULT: 'hsl(var(--muted) / <alpha-value>)',
          foreground: 'hsl(var(--muted-foreground) / <alpha-value>)'
        },
        accent: {
          DEFAULT: 'hsl(var(--accent) / <alpha-value>)',
          foreground: 'hsl(var(--accent-foreground) / <alpha-value>)'
        },
        destructive: {
        DEFAULT: 'hsl(var(--destructive) / <alpha-value>)',
          foreground: 'hsl(var(--destructive-foreground) / <alpha-value>)'
        },
      warning: {
        DEFAULT: 'hsl(var(--warning) / <alpha-value>)',
        foreground: 'hsl(var(--warning-foreground) / <alpha-value>)'
      },
      info: {
        DEFAULT: 'hsl(var(--info) / <alpha-value>)',
        foreground: 'hsl(var(--info-foreground) / <alpha-value>)'
      },
      'diff-add': 'hsl(var(--diff-add) / <alpha-value>)',
      'diff-del': 'hsl(var(--diff-del) / <alpha-value>)',
      success: {
        DEFAULT: 'hsl(var(--success) / <alpha-value>)',
        foreground: 'hsl(var(--success-foreground) / <alpha-value>)'
      },
        border: 'hsl(var(--border) / <alpha-value>)',
        input: 'hsl(var(--input) / <alpha-value>)',
        ring: 'hsl(var(--ring) / <alpha-value>)',
        chart: {
          '1': 'hsl(var(--chart-1) / <alpha-value>)',
          '2': 'hsl(var(--chart-2) / <alpha-value>)',
          '3': 'hsl(var(--chart-3) / <alpha-value>)',
          '4': 'hsl(var(--chart-4) / <alpha-value>)',
          '5': 'hsl(var(--chart-5) / <alpha-value>)'
        }
      },
      keyframes: {
        'accordion-down': {
          from: {
            height: '0'
          },
          to: {
            height: 'var(--radix-accordion-content-height)'
          }
        },
        'accordion-up': {
          from: {
            height: 'var(--radix-accordion-content-height)'
          },
          to: {
            height: '0'
          }
        }
      },
      animation: {
        'accordion-down': 'accordion-down 0.2s ease-out',
        'accordion-up': 'accordion-up 0.2s ease-out'
      }
    }
  },
  plugins: [tailwindcssAnimate, plugin(({ addUtilities }) => {
    // Background-only materials: text-* and border-* never inherit opacity.
    const surfaces = { page: 'background', panel: 'card', popover: 'popover', sidebar: 'appearance-sidebar', composer: 'card' }
    addUtilities(Object.fromEntries(Object.entries(surfaces).map(([surface, token]) => [`.bg-surface-${surface}`, {
      backgroundColor: `hsl(var(--${token}) / clamp(0, var(--aw-${surface}-opacity, 1), 1))`,
      backdropFilter: `var(--aw-${surface}-backdrop, none)`,
    }])))
  })],
}
