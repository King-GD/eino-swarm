import { fireEvent, render, screen } from "@testing-library/react"
import { expect, it, vi } from "vitest"
import { AudioParameterControls } from "./audio-parameters"
import type { AudioParameterView } from "../../lib/audio-parameters"
const view:AudioParameterView={scope:"one",version:1,revision:"r1",status:"configured",overrides:{},parameters:{voice:{values:["calm","bright"],default:"calm",configurable:true},speed:{min:0.1,max:10,default:2,configurable:true},response_format:{values:["pcm"],default:"pcm",configurable:false}}}
const labels={voice:"Voice",speed:"Speed",response_format:"Format",follow:"Follow gateway",invalid:"Choose a valid value",unavailable:"Unavailable"}
it("permits parameter selection independently of a fixed model and preserves an invalid saved voice",()=>{
 const change=vi.fn()
 const {container,rerender}=render(<AudioParameterControls title="Reading" view={view} role="speech" value={{voice:"gone"}} labels={labels} onChange={change} />)
 const details = container.querySelector("details")!
 expect(details.open).toBe(false)
 details.open = true
 expect(screen.getByRole("alert")).toHaveTextContent("Choose a valid value")
 expect(screen.getByLabelText("Voice")).toHaveValue("value:gone")
 expect(screen.queryByLabelText("Format")).toBeNull()
 fireEvent.change(screen.getByLabelText("Voice"),{target:{value:"value:bright"}})
 expect(change).toHaveBeenLastCalledWith({voice:"bright"})
 fireEvent.change(screen.getByLabelText("Speed (0.1–10)"),{target:{value:"8"}})
 expect(change).toHaveBeenLastCalledWith({voice:"gone",speed:8})
 fireEvent.click(screen.getByRole("button",{name:"Follow gateway"}))
 expect(change).toHaveBeenLastCalledWith({})
 rerender(<AudioParameterControls title="Reading" view={{...view,status:"incompatible",parameters:undefined}} role="speech" value={{voice:"gone"}} labels={labels} onChange={change} />)
 container.querySelector("details")!.open = true
 expect(screen.queryByLabelText("Voice")).toBeNull()
 expect(screen.getByRole("status")).toHaveTextContent("Unavailable")
})

it("omits the phone disclosure when the model only exposes fixed settings", () => {
 const parameters = { response_format: view.parameters!.response_format, speed: { min: 1, max: 1, default: 1, configurable: false } }
 const { container } = render(<AudioParameterControls title="Reading" view={{ ...view, parameters }} role="speech" value={{}} labels={labels} onChange={vi.fn()} />)
 expect(container).toBeEmptyDOMElement()
})
