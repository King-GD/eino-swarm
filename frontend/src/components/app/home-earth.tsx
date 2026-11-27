import { useEffect, useRef, useState } from "react"
import poster from "@/assets/fofa-earth-poster.png"

export function HomeEarth() {
  const mount = useRef<HTMLDivElement>(null)
  const [ready, setReady] = useState(false)

  useEffect(() => {
    const host = mount.current
    if (!host) return
    let active = true
    let dispose: (() => void) | undefined
    import("./render-home-earth").then(({ renderHomeEarth }) => {
      if (active) dispose = renderHomeEarth(host, value => { if (active) setReady(value) })
    }).catch(() => { if (active) setReady(false) })
    return () => { active = false; dispose?.() }
  }, [])

  return (
    <div className="wb-home-earth" aria-hidden="true" ref={mount}>
      {!ready && <img className="wb-earth-poster" src={poster} alt="" />}
    </div>
  )
}
