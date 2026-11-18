import { readFileSync } from "node:fs"
import path from "node:path"
import react from "@vitejs/plugin-react"
import { build, defineConfig } from "vite"
import { taskPanelBuild } from "../frontend/task-panel-build"

function shellVersion(): string {
  const pkg = JSON.parse(readFileSync(new URL("./package.json", import.meta.url), "utf8")) as {
    version?: unknown
  }
  return typeof pkg.version === "string" ? pkg.version : ""
}

export default defineConfig({
  define: {
    __ZWAI_WEB_VERSION__: JSON.stringify(shellVersion()),
  },
  plugins: [react(), taskPanelBuild(__dirname, build, path.resolve(__dirname, "../frontend/src/task-panels"))],
  resolve: {
    dedupe: ["react", "react-dom", "shiki", "mermaid", "mdast-util-from-markdown"],
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  server: { fs: { allow: [path.resolve(__dirname, "..")] } },
  build: { outDir: "dist", emptyOutDir: true, sourcemap: false },
})
