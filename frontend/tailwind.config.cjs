/** @type {import('tailwindcss').Config} */
const defaultTheme = require('tailwindcss/defaultTheme')

module.exports = {
  darkMode: ["class"],
  // Add light mode variant for dark-first design
  plugins: [
    require("tailwindcss-animate"),
    function({ addVariant }) {
      addVariant('light', '.light &')
    }
  ],
  content: [
    "./index.html",
    "./src/**/*.{vue,js,ts,jsx,tsx}",
  ],
  theme: {
  	// Replaces (not extends) Tailwind's radius scale so every rounded-* class
  	// comes from the --radius variables in src/assets/index.css. Tailwind's
  	// fixed sizes (rounded-xl, rounded-2xl, rounded-3xl) are intentionally absent.
  	borderRadius: {
  		none: '0px',
  		sm: 'var(--radius-sm)',
  		DEFAULT: 'var(--radius-md)',
  		md: 'var(--radius-md)',
  		lg: 'var(--radius)',
  		full: '9999px'
  	},
  	container: {
  		center: true,
  		padding: '2rem',
  		screens: {
  			'2xl': '1400px'
  		}
  	},
  	extend: {
  		fontFamily: {
  			sans: ['Inter', ...defaultTheme.fontFamily.sans],
  		},
  		colors: {
  			border: 'hsl(var(--border))',
  			input: 'hsl(var(--input))',
  			ring: 'hsl(var(--ring))',
  			background: 'hsl(var(--background))',
  			foreground: 'hsl(var(--foreground))',
  			primary: {
  				DEFAULT: 'hsl(var(--primary))',
  				foreground: 'hsl(var(--primary-foreground))'
  			},
  			secondary: {
  				DEFAULT: 'hsl(var(--secondary))',
  				foreground: 'hsl(var(--secondary-foreground))'
  			},
  			destructive: {
  				DEFAULT: 'hsl(var(--destructive))',
  				foreground: 'hsl(var(--destructive-foreground))'
  			},
  			muted: {
  				DEFAULT: 'hsl(var(--muted))',
  				foreground: 'hsl(var(--muted-foreground))'
  			},
  			accent: {
  				DEFAULT: 'hsl(var(--accent))',
  				foreground: 'hsl(var(--accent-foreground))'
  			},
  			popover: {
  				DEFAULT: 'hsl(var(--popover))',
  				foreground: 'hsl(var(--popover-foreground))'
  			},
  			card: {
  				DEFAULT: 'hsl(var(--card))',
  				foreground: 'hsl(var(--card-foreground))'
  			},
  			whatsapp: {
  				green: '#25D366',
  				teal: '#128C7E',
  				'teal-dark': '#075E54',
  				light: '#DCF8C6'
  			},
  			facebook: {
  				DEFAULT: '#1877F2',
  				dark: '#0C5DC7',
  				hover: '#166FE5',
  				hoverDark: '#0A4DAD'
  			},
  			// Violet color palette (primary accent)
  			violet: {
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
  				950: '#2e1065'
  			},
  			// Glass effect colors
  			glass: {
  				bg: 'var(--glass-bg)',
  				'bg-hover': 'var(--glass-bg-hover)',
  				border: 'var(--glass-border)'
  			}
  		},
  		keyframes: {
  			'accordion-down': {
  				from: {
  					height: '0'
  				},
  				to: {
  					height: 'var(--reka-accordion-content-height)'
  				}
  			},
  			'accordion-up': {
  				from: {
  					height: 'var(--reka-accordion-content-height)'
  				},
  				to: {
  					height: '0'
  				}
  			}
  		},
  		animation: {
  			'accordion-down': 'accordion-down 0.2s ease-out',
  			'accordion-up': 'accordion-up 0.2s ease-out'
  		},
  		// Glass effect backdrop blur
  		backdropBlur: {
  			xs: '2px'
  		}
  	}
  },
}
