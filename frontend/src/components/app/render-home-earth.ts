import {
  AmbientLight, DirectionalLight, DoubleSide, Mesh, MeshStandardMaterial, OrthographicCamera,
  RepeatWrapping, RingGeometry, Scene, SphereGeometry, TextureLoader, Vector3, WebGLRenderer,
} from "three"
import earthTexture from "@/assets/fofa-world-map.png"

// Camera, material, city coordinates and animation rates follow FOFA's components/Earth.
const radius = 390
const cities = [
  { latitude: 39.9042, longitude: 116.4074, cycle: 4 },
  { latitude: 51.5074, longitude: -0.1278, cycle: 4 },
  { latitude: 34.0522, longitude: -118.2437, cycle: 6 },
]

export function renderHomeEarth(host: HTMLElement, showCanvas: (ready: boolean) => void) {
  let renderer: WebGLRenderer
  try {
    renderer = new WebGLRenderer({ antialias: true, alpha: true })
  } catch {
    return
  }
  const canvas = renderer.domElement
  host.appendChild(canvas)
  const scene = new Scene()
  const camera = new OrthographicCamera(-radius, radius, radius, 0, 0.1, 10000)
  camera.position.z = 1000
  const material = new MeshStandardMaterial({ transparent: true, side: DoubleSide, depthWrite: false })
  const globe = new Mesh(new SphereGeometry(radius, 192, 128), material)
  globe.rotation.y = -110 * Math.PI / 180
  scene.add(globe)

  const ambient = new AmbientLight()
  const sun = new DirectionalLight(0xffffff, 1)
  sun.position.set(5, 5, 5)
  scene.add(ambient, sun)
  const rings = cities.map(({ latitude, longitude, cycle }, index) => {
    const phi = (90 - latitude) * Math.PI / 180
    const theta = (longitude + 180) * Math.PI / 180
    const normal = new Vector3(-Math.sin(phi) * Math.cos(theta), Math.cos(phi), Math.sin(phi) * Math.sin(theta))
    const ring = new Mesh(new RingGeometry(1, 2, 48), new MeshStandardMaterial({
      color: 0x00ffff, transparent: true, side: DoubleSide, depthWrite: false,
    }))
    ring.position.copy(normal).multiplyScalar(radius + 1.5)
    ring.quaternion.setFromUnitVectors(new Vector3(0, 0, 1), normal)
    globe.add(ring)
    return { ring, cycle, phase: index * 0.65 + 0.2 }
  })

  const motion = window.matchMedia("(prefers-reduced-motion: reduce)")
  let disposed = false
  let loaded = false
  let lost = false
  let frame = 0
  let elapsed = 0
  let previous = 0
  function draw() {
    if (disposed || !loaded || lost || host.clientHeight === 0 || document.hidden) return
    globe.rotation.y = -110 * Math.PI / 180 + elapsed * 0.024
    for (const { ring, cycle, phase } of rings) {
      const progress = ((elapsed * 0.6 + phase) % cycle) / cycle
      const inner = 1 + progress * 35
      // Scale a unit annulus, keeping its stroke one scene unit wide.
      const positions = ring.geometry.attributes.position
      const uv = ring.geometry.attributes.uv
      for (let i = 0; i < positions.count; i++) {
        const r = i < positions.count / 2 ? inner : inner + 1
        const x = uv.getX(i) - 0.5
        const y = uv.getY(i) - 0.5
        const scale = r / Math.hypot(x, y)
        positions.setXY(i, x * scale, y * scale)
      }
      positions.needsUpdate = true
      ring.frustumCulled = false
      ring.material.opacity = 0.7 * (1 - progress)
    }
    renderer.render(scene, camera)
  }
  function tick(now: number) {
    elapsed += previous ? Math.min((now - previous) / 1000, 0.1) : 0
    previous = now
    draw()
    frame = requestAnimationFrame(tick)
  }
  function refresh() {
    cancelAnimationFrame(frame)
    previous = 0
    if (disposed || lost || !loaded || document.hidden || host.clientHeight === 0) return
    const w = host.clientWidth, h = host.clientHeight
    camera.left = -radius
    camera.right = radius
    camera.top = radius
    camera.bottom = radius - 2 * radius * h / w
    camera.updateProjectionMatrix()
    renderer.setSize(w, h, false)
    const light = !document.documentElement.classList.contains("dark")
    material.color.setHex(light ? 0x555555 : 0xffffff)
    ambient.intensity = w <= 280 ? 5.5 : light ? 3 : 0.5
    draw()
    showCanvas(true)
    if (!motion.matches) frame = requestAnimationFrame(tick)
  }
  function contextLost() {
    lost = true
    cancelAnimationFrame(frame)
    showCanvas(false)
  }
  function restored() { lost = false; refresh() }
  canvas.addEventListener("webglcontextlost", contextLost)
  canvas.addEventListener("webglcontextrestored", restored)
  const texture = new TextureLoader().load(earthTexture, () => {
    if (disposed) return
    texture.wrapS = texture.wrapT = RepeatWrapping
    material.map = texture
    material.needsUpdate = true
    loaded = true
    refresh()
  })
  const resize = new ResizeObserver(refresh)
  resize.observe(host)
  const theme = new MutationObserver(refresh)
  theme.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] })
  motion.addEventListener("change", refresh)
  document.addEventListener("visibilitychange", refresh)
  return () => {
    disposed = true
    cancelAnimationFrame(frame)
    resize.disconnect()
    theme.disconnect()
    motion.removeEventListener("change", refresh)
    document.removeEventListener("visibilitychange", refresh)
    canvas.removeEventListener("webglcontextlost", contextLost)
    canvas.removeEventListener("webglcontextrestored", restored)
    for (const { ring } of rings) { ring.geometry.dispose(); ring.material.dispose() }
    globe.geometry.dispose()
    material.dispose()
    texture.dispose()
    renderer.dispose()
    renderer.forceContextLoss()
    canvas.remove()
  }
}
