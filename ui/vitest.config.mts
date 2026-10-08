import {fileURLToPath} from "url"
import {defineConfig} from "vitest/config"

export default defineConfig({
  resolve: {alias: {"@": fileURLToPath(new URL("./src", import.meta.url))}},
  test: {
    environment: "jsdom",
    globals: true,
    include: ["src/**/*.test.{ts,tsx}"],
    // @aoctech/ui ships ESM with extensionless relative imports, which Node's
    // resolver refuses; inlining lets Vite resolve them like the app build does.
    server: {deps: {inline: ["@aoctech/ui"]}},
  },
})
