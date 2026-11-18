import { expect, it } from "vitest"
import world from "./world.json"

it("gives every geometry an independent stable unique identity", () => {
  const ids = world.features.map((feature) => (feature.properties as { geometry_id?: string }).geometry_id)
  expect(ids.every((id) => typeof id === "string" && id.startsWith("ne:"))).toBe(true)
  expect(new Set(ids).size).toBe(world.features.length)
})

it("uses source ISO fields without confusing postal abbreviations with country codes", () => {
  const names = (code: string) => world.features.filter((feature) => feature.properties.code === code).map((feature) => feature.properties.name)
  expect(names("CN")).toEqual(["People's Republic of China"])
  expect(names("SL")).toEqual(["Sierra Leone"])
  expect(names("FR")).toEqual(["France"])
  expect(names("NO")).toEqual(["Norway"])
  for (const name of ["Somaliland", "Turkish Republic of Northern Cyprus"]) {
    expect(world.features.find((feature) => feature.properties.name === name)?.properties.code).toBe("")
  }
})
