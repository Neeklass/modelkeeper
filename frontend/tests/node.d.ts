// Just enough of node:test and node:assert for the tests (no @types/node
// dependency).
declare module 'node:test' {
    export function test(name: string, fn: () => void | Promise<void>): void
}
declare module 'node:assert' {
    export const strict: {
        equal(actual: unknown, expected: unknown, message?: string): void
        deepEqual(actual: unknown, expected: unknown, message?: string): void
        ok(value: unknown, message?: string): void
    }
}
