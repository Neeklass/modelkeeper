// 3D preview (concept H4, B7). Loaded on demand, so three.js is only fetched
// when a preview is opened.
import * as THREE from 'three'
import {OrbitControls} from 'three/examples/jsm/controls/OrbitControls.js'
import {STLLoader} from 'three/examples/jsm/loaders/STLLoader.js'
import {PLYLoader} from 'three/examples/jsm/loaders/PLYLoader.js'
import {OBJLoader} from 'three/examples/jsm/loaders/OBJLoader.js'
import {ThreeMFLoader} from 'three/examples/jsm/loaders/3MFLoader.js'
import {upAxis, orientMatrix} from './orient'

// Same look as rendered thumbnails (internal/thumbs/render.go).
const BACKGROUND = 0x3a3c44
const MODEL_COLOR = 0xc9cdd6
// Initial view: turned 30° and looking down 20°, like the thumbnails.
const YAW = 30 * Math.PI / 180
const PITCH = 20 * Math.PI / 180

/** Turns file content into an object (meshes in the file's own coordinates). */
export function parseModel(ext: string, data: ArrayBuffer): THREE.Object3D {
    switch (ext) {
        case 'stl':
            return new THREE.Mesh(new STLLoader().parse(data))
        case 'ply': {
            const g = new PLYLoader().parse(data)
            if (!g.getAttribute('normal')) g.computeVertexNormals()
            return new THREE.Mesh(g)
        }
        case 'obj':
            return new OBJLoader().parse(new TextDecoder().decode(data))
        case '3mf':
            return new ThreeMFLoader().parse(data)
    }
    throw new Error(`unsupported file type: ${ext}`)
}

export class Viewer {
    private renderer: THREE.WebGLRenderer
    private scene = new THREE.Scene()
    private camera = new THREE.PerspectiveCamera(35, 1, 0.1, 1000)
    private controls: OrbitControls
    private material = new THREE.MeshStandardMaterial({
        color: MODEL_COLOR, roughness: 0.65, metalness: 0.05, side: THREE.DoubleSide,
    })
    private model: THREE.Object3D | null = null
    private resize: ResizeObserver
    private frame = 0

    constructor(private host: HTMLElement) {
        // preserveDrawingBuffer: the canvas can be captured as thumbnail (I4).
        this.renderer = new THREE.WebGLRenderer({antialias: true, preserveDrawingBuffer: true})
        this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
        this.renderer.setClearColor(BACKGROUND)
        host.appendChild(this.renderer.domElement)

        this.scene.add(new THREE.HemisphereLight(0xffffff, 0x50525a, 1.6))
        const key = new THREE.DirectionalLight(0xffffff, 2.2)
        key.position.set(-0.5, 1, 1) // relative to the camera: upper left, like the thumbnails
        this.camera.add(key)
        this.scene.add(this.camera)

        this.controls = new OrbitControls(this.camera, this.renderer.domElement)
        this.controls.enableDamping = true
        this.controls.addEventListener('change', () => this.requestRender())

        this.resize = new ResizeObserver(() => this.fit())
        this.resize.observe(host)
        this.fit()
    }

    /** Shows a parsed model, oriented upright and centered. */
    show(obj: THREE.Object3D) {
        this.clearModel()
        obj.updateMatrixWorld(true)
        const meshes: THREE.Mesh[] = []
        obj.traverse(o => {
            if ((o as THREE.Mesh).isMesh) meshes.push(o as THREE.Mesh)
        })
        if (meshes.length === 0) throw new Error('no meshes in file')
        for (const m of meshes) {
            disposeMaterial(m.material)
            m.material = this.material
        }

        // Orientation guess as for thumbnails, then from Z-up to three.js' Y-up.
        const up = upAxis(worldPositions(meshes))
        const holder = new THREE.Group()
        holder.applyMatrix4(new THREE.Matrix4().makeRotationX(-Math.PI / 2).multiply(orientMatrix(up)))
        holder.add(obj)
        // Put the model on the ground, centered.
        holder.updateMatrixWorld(true)
        const box = new THREE.Box3().setFromObject(holder)
        if (box.isEmpty() || !isFinite(box.max.x - box.min.x)) throw new Error('empty model')
        const center = box.getCenter(new THREE.Vector3())
        holder.position.sub(new THREE.Vector3(center.x, box.min.y, center.z))

        this.model = holder
        this.scene.add(holder)
        this.resetView()
    }

    /** Back to the initial camera position. */
    resetView() {
        if (!this.model) return
        const box = new THREE.Box3().setFromObject(this.model)
        const sphere = box.getBoundingSphere(new THREE.Sphere())
        const r = Math.max(sphere.radius, 1e-6)
        const dist = r / Math.sin(THREE.MathUtils.degToRad(this.camera.fov / 2)) * 1.05
        const dir = new THREE.Vector3(Math.sin(YAW) * Math.cos(PITCH), Math.sin(PITCH), Math.cos(YAW) * Math.cos(PITCH))
        this.camera.near = dist / 100
        this.camera.far = dist * 100
        this.camera.position.copy(sphere.center).addScaledVector(dir, dist)
        this.camera.updateProjectionMatrix()
        this.controls.target.copy(sphere.center)
        this.controls.minDistance = r * 0.05
        this.controls.maxDistance = dist * 20
        this.controls.update()
        this.requestRender()
    }

    /** The current view as a square JPEG data URL (for the thumbnail, I4). */
    capture(): string {
        this.renderer.render(this.scene, this.camera)
        const src = this.renderer.domElement
        const side = Math.min(src.width, src.height)
        const out = document.createElement('canvas')
        out.width = out.height = side
        out.getContext('2d')!.drawImage(src, (src.width - side) / 2, (src.height - side) / 2, side, side, 0, 0, side, side)
        return out.toDataURL('image/jpeg', 0.92)
    }

    dispose() {
        cancelAnimationFrame(this.frame)
        this.resize.disconnect()
        this.controls.dispose()
        this.clearModel()
        this.material.dispose()
        this.renderer.dispose()
        this.renderer.forceContextLoss()
        this.renderer.domElement.remove()
    }

    private clearModel() {
        if (!this.model) return
        this.scene.remove(this.model)
        this.model.traverse(o => {
            const m = o as THREE.Mesh
            if (m.isMesh) m.geometry.dispose()
        })
        this.model = null
    }

    private fit() {
        const w = Math.max(1, this.host.clientWidth), h = Math.max(1, this.host.clientHeight)
        this.renderer.setSize(w, h, false)
        this.camera.aspect = w / h
        this.camera.updateProjectionMatrix()
        this.requestRender()
    }

    // Renders on demand; keeps going while the damped controls are moving.
    private requestRender() {
        if (this.frame) return
        this.frame = requestAnimationFrame(() => {
            this.frame = 0
            const moving = this.controls.update() // emits 'change' while damping
            this.renderer.render(this.scene, this.camera)
            if (moving) this.requestRender()
        })
    }
}

function disposeMaterial(m: THREE.Material | THREE.Material[]) {
    for (const x of Array.isArray(m) ? m : [m]) x.dispose()
}

/** Vertex positions of all meshes in world space, flat xyz. */
function worldPositions(meshes: THREE.Mesh[]): Float32Array {
    let n = 0
    for (const m of meshes) n += m.geometry.getAttribute('position').count
    const out = new Float32Array(n * 3)
    const v = new THREE.Vector3()
    let i = 0
    for (const m of meshes) {
        const pos = m.geometry.getAttribute('position')
        for (let k = 0; k < pos.count; k++) {
            v.fromBufferAttribute(pos, k).applyMatrix4(m.matrixWorld)
            out[i++] = v.x
            out[i++] = v.y
            out[i++] = v.z
        }
    }
    return out
}
