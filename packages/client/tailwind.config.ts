import type { Config } from "tailwindcss";

export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        bg: "#080808",
        surface: "#0d0d0d",
        "surface-2": "#121212",
        "surface-3": "#181818",
        border: "#1e1e1e",
        "border-2": "#2a2a2a",
        tx: "#ebebeb",
        "tx-2": "#999999",
        "tx-3": "#555555",
        accent: "#4d7fff",
        "accent-dim": "#1a2d5a",
      },
      fontFamily: {
        ui: ["Outfit", "sans-serif"],
        mono: ["JetBrains Mono", "Menlo", "monospace"],
      },
      fontSize: {
        "2xs": ["10px", "14px"],
      },
    },
  },
  plugins: [],
} satisfies Config;
