import { fireEvent, render, screen } from "@testing-library/react"
import { expect, it, vi } from "vitest"
import { AudioParameterControls } from "./audio-parameters"
import type { AudioParameterView } from "../../lib/audio-parameters"
const view:AudioParameterView={scope:"one",version:1,revision:"r1",status:"configured",overrides:{},parameters:{voice:{values:["calm","bright"],default:"calm",configurable:true},speed:{min:0.1,max:10,default:2,configurable:true},response_format:{values:["pcm"],default:"pcm",configurable:false}}}
const labels={voice:"Voice",speed:"Speed",response_format:"Format",follow:"Follow gateway",invalid:"Choose a valid value",unavailable:"Unavailable"}
it("permits parameter selection independently of a fixed model and preserves an invalid saved voice",()=>{
 const change=vi.fn()
 const {rerender}=render(<AudioParameterControls view={view} role="speech" value={{voice:"gone"}} labels={labels} onChange={change} />)
 expect(screen.getByRole("alert")).toHaveTextContent("Choose a valid value")
 expect(screen.getByLabelText("Voice")).toHaveValue("value:gone")
 expect(screen.getByLabelText("Format")).toBeDisabled()
 fireEvent.change(screen.getByLabelText("Voice"),{target:{value:"value:bright"}})
 expect(change).toHaveBeenLastCalledWith({voice:"bright"})
 fireEvent.change(screen.getByLabelText("Speed (0.1–10)"),{target:{value:"8"}})
 expect(change).toHaveBeenLastCalledWith({voice:"gone",speed:8})
 fireEvent.click(screen.getByRole("button",{name:"Follow gateway"}))
 expect(change).toHaveBeenLastCalledWith({})
 rerender(<AudioParameterControls view={{...view,status:"incompatible",parameters:undefined}} role="speech" value={{voice:"gone"}} labels={labels} onChange={change} />)
 expect(screen.queryByLabelText("Voice")).toBeNull()
 expect(screen.getByRole("status")).toHaveTextContent("Unavailable")
})
