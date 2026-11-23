import { describe, expect, it } from "vitest"

import { chooseWorkspaceFile, workspaceFilePath } from "./workspace-file-ref"

describe("workspace file paths in an answer", () => {
  it("accepts a relative previewable file and a bare name", () => {
    expect(workspaceFilePath("report/list/out.md")).toBe("report/list/out.md")
    expect(workspaceFilePath("./report/list/out.md")).toBe("report/list/out.md")
    expect(workspaceFilePath("rows.tsv")).toBe("rows.tsv")
    expect(workspaceFilePath("报告/清单.md")).toBe("报告/清单.md")
    expect(workspaceFilePath("shot.png")).toBe("shot.png")
    expect(workspaceFilePath("book.docx")).toBe("book.docx")
  })

  it("rejects urls, directories, traversal and names the preview cannot open", () => {
    expect(workspaceFilePath("https://example.invalid/docs/guide.md")).toBeNull()
    expect(workspaceFilePath("example.com/docs/guide.md")).toBeNull()
    expect(workspaceFilePath("/tmp/guide.md")).toBeNull()
    expect(workspaceFilePath("../guide.md")).toBeNull()
    expect(workspaceFilePath("dir/")).toBeNull()
    expect(workspaceFilePath("notes/../guide.md")).toBeNull()
    expect(workspaceFilePath("archive.tar.gz")).toBeNull()
    expect(workspaceFilePath("readme")).toBeNull()
  })
})

describe("chooseWorkspaceFile", () => {
  const file = (path: string) => ({ path, dir: false })

  it("keeps a path that already matches the file tree", () => {
    expect(chooseWorkspaceFile("report/list/out.md", [
      file("report/list/out.md"),
      file("work/report/list/out.md"),
    ])).toBe("report/list/out.md")
  })

  it("uses the task work directory when the answer omitted it", () => {
    expect(chooseWorkspaceFile("notes/a.md", [
      file("work/notes/a.md"),
      { path: "work/notes", dir: true },
    ])).toBe("work/notes/a.md")
  })

  it("uses one deeper match and does not treat a similar name as the file", () => {
    expect(chooseWorkspaceFile("a.md", [file("pkg/a.md")])).toBe("pkg/a.md")
    expect(chooseWorkspaceFile("a.md", [file("data.md"), file("folder/data.md")])).toBe("a.md")
    expect(chooseWorkspaceFile("a.md", [file("pkg/a.md"), file("other/a.md")])).toBe("a.md")
  })
})
