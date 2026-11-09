import {defineConfig} from 'vite'
import react from '@vitejs/plugin-react'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  // three.js (3D preview) is a separate chunk of ~560 kB, loaded on demand.
  build: {chunkSizeWarningLimit: 700},
})
