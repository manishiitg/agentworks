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
  			background: 'hsl(var(--background))',
  			foreground: 'hsl(var(--foreground))',
  			card: {
  				DEFAULT: 'hsl(var(--card))',
  				foreground: 'hsl(var(--card-foreground))'
  			},
  			popover: {
  				DEFAULT: 'hsl(var(--popover))',
  				foreground: 'hsl(var(--popover-foreground))'
  			},
  			primary: {
  				DEFAULT: 'hsl(var(--primary))',
  				foreground: 'hsl(var(--primary-foreground))'
  			},
  			secondary: {
  				DEFAULT: 'hsl(var(--secondary))',
  				foreground: 'hsl(var(--secondary-foreground))'
  			},
  			muted: {
  				DEFAULT: 'hsl(var(--muted))',
  				foreground: 'hsl(var(--muted-foreground))'
  			},
  			accent: {
  				DEFAULT: 'hsl(var(--accent))',
  				foreground: 'hsl(var(--accent-foreground))'
  			},
  			destructive: {
				DEFAULT: 'hsl(var(--destructive) / <alpha-value>)',
  				foreground: 'hsl(var(--destructive-foreground))'
  			},
			warning: {
				DEFAULT: 'hsl(var(--warning) / <alpha-value>)',
				foreground: 'hsl(var(--foreground))'
			},
			info: {
				DEFAULT: 'hsl(var(--info) / <alpha-value>)',
				foreground: 'hsl(var(--foreground))'
			},
			'diff-add': 'hsl(var(--diff-add) / <alpha-value>)',
			'diff-del': 'hsl(var(--diff-del) / <alpha-value>)',
			success: {
				DEFAULT: 'hsl(var(--success) / <alpha-value>)',
				foreground: 'hsl(var(--foreground))'
			},
  			border: 'hsl(var(--border))',
  			input: 'hsl(var(--input))',
  			ring: 'hsl(var(--ring))',
  			chart: {
  				'1': 'hsl(var(--chart-1))',
  				'2': 'hsl(var(--chart-2))',
  				'3': 'hsl(var(--chart-3))',
  				'4': 'hsl(var(--chart-4))',
  				'5': 'hsl(var(--chart-5))'
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