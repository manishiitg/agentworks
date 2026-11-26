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
      // One shadow colour per mode (--shadow-color, --shadow-strength in app-tokens.css).
      // Dark keeps Tailwind's own black shadows; light uses a softer, cool tint.
      boxShadow: (() => {
        const c = k => `hsl(var(--shadow-color, 0 0% 0%) / calc(var(--shadow-strength, 0.1) * ${k}))`
        return {
          sm: `0 1px 2px 0 ${c(0.5)}`,
          DEFAULT: `0 1px 3px 0 ${c(1)}, 0 1px 2px -1px ${c(1)}`,
          md: `0 4px 6px -1px ${c(1)}, 0 2px 4px -2px ${c(1)}`,
          lg: `0 10px 15px -3px ${c(1)}, 0 4px 6px -4px ${c(1)}`,
          xl: `0 20px 25px -5px ${c(1)}, 0 8px 10px -6px ${c(1)}`,
          '2xl': `0 25px 50px -12px ${c(2.5)}`,
        }
      })(),
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
  plugins: [tailwindcssAnimate],
}
