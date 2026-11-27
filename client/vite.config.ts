import babel from "@rolldown/plugin-babel";
import prefresh from "@prefresh/vite";
import { defineConfig } from "vite";
import react, { reactCompilerPreset } from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// react is preact underneath. react's fast refresh needs react itself, so prefresh does it instead
const compat = [
  "react-dom/client",
  "react-dom",
  "react/jsx-runtime",
  "react/jsx-dev-runtime",
  "react/compiler-runtime",
  "react",
];

export default defineConfig({
  plugins: [
    react({ refreshExclude: /./ }),
    await prefresh(),
    babel({ presets: [reactCompilerPreset()] }),
    tailwindcss(),
  ],
  resolve: {
    alias: compat.map((m) => ({
      find: new RegExp(`^${m}$`),
      replacement: m.replace(/^react(-dom)?/, "preact/compat"),
    })),
  },
});
