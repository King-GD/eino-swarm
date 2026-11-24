import { readFile } from "node:fs/promises"
import path from "node:path"
import type { build as viteBuild, Plugin } from "vite"

interface Definition { id: string; entry: string; renderer?: string }
const csp = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; connect-src 'none'; worker-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"

/** Produce self-contained plugin documents; opaque iframe origins need no CORS. */
export function taskPanelBuild(root: string, build: typeof viteBuild, directory = path.join(root, "src/task-panels")): Plugin {
  async function definitions(): Promise<Definition[]> {
    const catalog: Definition[] = JSON.parse(await readFile(path.join(directory, "catalog.json"), "utf8"))
    // Native renderers ship inside the app bundle and have no plugin document.
    return catalog.filter((definition) => definition.renderer !== "native")
  }
  async function document(definition: Definition): Promise<string> {
    if (!/^[a-z0-9-]+$/.test(definition.id) || definition.entry !== `task-panels/${definition.id}.html`) throw new Error("Invalid task panel build entry")
    const result = await build({
      configFile: false,
      root,
      logLevel: "warn",
      resolve: { dedupe: ["d3"] },
      build: {
        write: false, emptyOutDir: false, minify: true,
        lib: { entry: path.join(directory, definition.id, "main.ts"), name: "TaskPanelPlugin", formats: ["iife"] },
        rollupOptions: { output: { inlineDynamicImports: true } },
      },
    })
    const bundles = Array.isArray(result) ? result : [result]
    let script = "", css = ""
    for (const bundle of bundles) {
      if (!("output" in bundle)) throw new Error("Unexpected plugin watch build")
      for (const item of bundle.output) {
        if (item.type === "chunk") script += item.code
        else if (item.fileName.endsWith(".css")) css += String(item.source)
        else throw new Error(`Plugin asset must be bundled inline: ${item.fileName}`)
      }
    }
    const html = await readFile(path.join(directory, definition.id, "index.html"), "utf8")
    // Replacement callbacks preserve literal $&, $` and $' inside bundled JS.
    return html.replace("<head>", () => `<head><meta http-equiv="Content-Security-Policy" content="${csp}">`)
      .replace("</head>", () => `<style>${css.replaceAll("</style", "<\\/style")}</style></head>`)
      .replace("</body>", () => `<script>${script.replaceAll("</script", "<\\/script")}</script></body>`)
  }
  return {
    name: "task-panel-documents",
    async generateBundle() {
      for (const definition of await definitions()) this.emitFile({ type: "asset", fileName: definition.entry, source: await document(definition) })
    },
    configureServer(server) {
      server.middlewares.use(async (request, response, next) => {
        if (!request.url?.startsWith("/task-panels/")) { next(); return }
        try {
          const definition = (await definitions()).find((item) => `/${item.entry}` === request.url?.split("?")[0])
          if (!definition) { response.statusCode = 404; response.end(); return }
          response.setHeader("Content-Type", "text/html; charset=utf-8")
          response.end(await document(definition))
        } catch (error) { next(error) }
      })
    },
  }
}
