// Orientation guess for the 3D preview: a port of upAxis/axisScores from
// internal/thumbs/render.go, so preview and rendered thumbnail stand the same way.
//
// Orientation codes: which axis points up. 0=+Z, 1=-Z, 2=+Y, 3=-Y, 4=+X, 5=-X.
import {Matrix4} from 'three'

/** orient() of render.go: rotates a vertex so the given axis points up (+Z). */
export function orient(x: number, y: number, z: number, up: number): [number, number, number] {
    switch (up) {
        case 1: return [x, -y, -z]
        case 2: return [x, -z, y]
        case 3: return [x, z, -y]
        case 4: return [-z, y, x]
        case 5: return [z, y, -x]
    }
    return [x, y, z]
}

/** The orient() rotation as a matrix. */
export function orientMatrix(up: number): Matrix4 {
    const [ax, ay, az] = orient(1, 0, 0, up)
    const [bx, by, bz] = orient(0, 1, 0, up)
    const [cx, cy, cz] = orient(0, 0, 1, up)
    return new Matrix4().set(
        ax, bx, cx, 0,
        ay, by, cy, 0,
        az, bz, cz, 0,
        0, 0, 0, 1,
    )
}

// Larger meshes are sampled: the guess needs the shape, not every vertex.
const MAX_SAMPLES = 400_000

/** Guesses the up axis from vertex positions (flat xyz); see upAxis in render.go. */
export function upAxis(pos: ArrayLike<number>): number {
    const confident = 0.25
    const count = Math.floor(pos.length / 3)
    const stride = Math.max(1, Math.ceil(count / MAX_SAMPLES))
    const scores = axisScores(pos, count, stride)
    let best = 0
    for (let up = 1; up < 6; up++) if (scores[up] > scores[best]) best = up
    if (scores[best] >= confident) return best

    // Longest extent points up; the side with more contact is the bottom.
    const min = [Infinity, Infinity, Infinity], max = [-Infinity, -Infinity, -Infinity]
    for (let i = 0; i < count; i += stride) {
        for (let k = 0; k < 3; k++) {
            const v = pos[i * 3 + k]
            if (v < min[k]) min[k] = v
            if (v > max[k]) max[k] = v
        }
    }
    const ex = max[0] - min[0], ey = max[1] - min[1], ez = max[2] - min[2]
    let pair = 0 // Z
    if (ey > ez && ey >= ex) pair = 2
    else if (ex > ez && ex > ey) pair = 4
    return scores[pair + 1] > scores[pair] ? pair + 1 : pair
}

/** Spread of the touching points relative to the footprint, per orientation. */
export function axisScores(pos: ArrayLike<number>, count = Math.floor(pos.length / 3), stride = 1): number[] {
    const band = 0.02 // vertices within 2% of the height count as touching the ground
    const scores = [0, 0, 0, 0, 0, 0]
    for (let up = 0; up < 6; up++) {
        let x0 = Infinity, y0 = Infinity, z0 = Infinity, x1 = -Infinity, y1 = -Infinity, z1 = -Infinity
        for (let i = 0; i < count; i += stride) {
            const [x, y, z] = orient(pos[i * 3], pos[i * 3 + 1], pos[i * 3 + 2], up)
            if (x < x0) x0 = x
            if (x > x1) x1 = x
            if (y < y0) y0 = y
            if (y > y1) y1 = y
            if (z < z0) z0 = z
            if (z > z1) z1 = z
        }
        const footprint = (x1 - x0) * (y1 - y0)
        if (!(footprint > 0)) continue
        const limit = z0 + band * (z1 - z0)
        let cx0 = Infinity, cy0 = Infinity, cx1 = -Infinity, cy1 = -Infinity
        for (let i = 0; i < count; i += stride) {
            const [x, y, z] = orient(pos[i * 3], pos[i * 3 + 1], pos[i * 3 + 2], up)
            if (z <= limit) {
                if (x < cx0) cx0 = x
                if (x > cx1) cx1 = x
                if (y < cy0) cy0 = y
                if (y > cy1) cy1 = y
            }
        }
        scores[up] = (cx1 - cx0) * (cy1 - cy0) / footprint
    }
    return scores
}
