import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "./app/**/*.{ts,tsx}",
    "./components/**/*.{ts,tsx}",
    "./lib/**/*.{ts,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        ink: "#0f1720",
        mist: "#f6f1e8",
        ember: "#e07a2f",
        sand: "#eadbc7",
        spruce: "#1e5c56",
        slatepanel: "#15202b",
      },
      fontFamily: {
        sans: ["Avenir Next", "Helvetica Neue", "Segoe UI", "sans-serif"],
        display: ["Iowan Old Style", "Palatino Linotype", "Book Antiqua", "serif"],
        mono: ["SFMono-Regular", "Menlo", "Monaco", "monospace"],
      },
      boxShadow: {
        panel: "0 24px 60px rgba(18, 25, 34, 0.12)",
      },
      backgroundImage: {
        "hero-glow":
          "radial-gradient(circle at top left, rgba(224, 122, 47, 0.22), transparent 28%), radial-gradient(circle at top right, rgba(30, 92, 86, 0.18), transparent 22%), linear-gradient(180deg, rgba(255,255,255,0.96), rgba(249,244,236,0.88))",
      },
    },
  },
  plugins: [],
};

export default config;
