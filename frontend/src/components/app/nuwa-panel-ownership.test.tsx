import { useLayoutEffect, useRef } from "react"
import { act, render, screen } from "@testing-library/react"
import { expect, it, vi } from "vitest"
import { nuwaAPI, subscribeNuwa } from "@/lib/nuwa-api"
import type { Project } from "@/lib/types"
import { NuwaPanel } from "./nuwa-panel"

vi.mock("@/lib/nuwa-api", () => ({ nuwaAPI: { snapshot: vi.fn() }, subscribeNuwa: vi.fn() }))

const first: Project = {
  id: "old", name: "Previous project", system_prompt: "", workdir: "", resolved_workdir: "",
  memory_enabled: false, memory_dir: "", nuwa_enabled: true, created_at: "", updated_at: "",
}
const second = { ...first, id: "new", name: "Next project" }

it("does not commit the previous project's error or Live indicator under the next project", async () => {
  vi.mocked(nuwaAPI.snapshot).mockImplementation(async (id) => {
    if (id === first.id) throw new Error("Previous project snapshot failed")
    return { project_id: id, revision: 0, nodes: [], workers: [] }
  })
  vi.mocked(subscribeNuwa).mockImplementation((id, events) => {
    if (id === first.id) events.onOpen()
    return () => {}
  })
  const commits: { heading: string | null; alert: string | null; live: boolean; refreshDisabled: boolean }[] = []
  function ObserveCommit({ project }: { project: Project }) {
    const container = useRef<HTMLDivElement>(null)
    useLayoutEffect(() => {
      const root = container.current!
      commits.push({
        heading: root.querySelector("h2")?.textContent ?? null,
        alert: root.querySelector('[role="alert"]')?.textContent ?? null,
        live: [...root.querySelectorAll("span")].some((element) => element.textContent === "Live"),
        refreshDisabled: root.querySelector<HTMLButtonElement>('button[aria-label="Refresh shared work"]')?.disabled === true,
      })
    }, [project.id])
    return <div ref={container}><NuwaPanel project={project} onSelectAgent={() => {}} /></div>
  }

  const view = render(<ObserveCommit project={first} />)
  expect(await screen.findByRole("alert")).toHaveTextContent("Previous project snapshot failed")
  expect(screen.getByText("Live")).toBeVisible()

  await act(async () => view.rerender(<ObserveCommit project={second} />))
  expect(commits.at(-1)).toEqual({ heading: second.name, alert: null, live: false, refreshDisabled: true })
  expect(nuwaAPI.snapshot).toHaveBeenCalledWith(second.id)
})
