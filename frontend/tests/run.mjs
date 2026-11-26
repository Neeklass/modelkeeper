// npm test: compile the tests (and what they import) to CommonJS with the
// project's TypeScript, then run them with Node's built-in test runner.
import {execFileSync} from 'node:child_process'
import {mkdirSync, readdirSync, rmSync, writeFileSync} from 'node:fs'
import {createRequire} from 'node:module'

const require = createRequire(import.meta.url)
rmSync('.test-out', {recursive: true, force: true})
execFileSync(process.execPath, [require.resolve('typescript/bin/tsc'), '-p', 'tests/tsconfig.json'], {stdio: 'inherit'})
mkdirSync('.test-out', {recursive: true})
writeFileSync('.test-out/package.json', '{"type": "commonjs"}\n')
const files = readdirSync('.test-out/tests').filter(f => f.endsWith('.test.js')).map(f => `.test-out/tests/${f}`)
execFileSync(process.execPath, ['--test', ...files], {stdio: 'inherit'})
