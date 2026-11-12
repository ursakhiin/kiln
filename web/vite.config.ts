import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    // Built files are embedded into the Go binary by internal/webui.
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
})
