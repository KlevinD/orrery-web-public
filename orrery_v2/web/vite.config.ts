import tailwindcss from '@tailwindcss/vite'
import adapter from '@sveltejs/adapter-static'
import { sveltekit } from '@sveltejs/kit/vite'
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'
import { defineConfig, loadEnv } from 'vite'

const defaultApiProxyTarget = 'http://127.0.0.1:8080'

export function resolveApiProxyTarget(env: Record<string, string | undefined> = process.env) {
  return env.VITE_API_PROXY_TARGET?.trim() || defaultApiProxyTarget
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const proxyTarget = resolveApiProxyTarget({ ...env, ...process.env })

  return {
    plugins: [
      tailwindcss(),
      sveltekit({
        adapter: adapter({
          pages: process.env.ORRERY_WEB_BUILD_DIR || 'build',
          assets: process.env.ORRERY_WEB_BUILD_DIR || 'build',
          fallback: 'index.html'
        }),
        preprocess: vitePreprocess()
      })
    ],
    optimizeDeps: {
      include: ['qrcode']
    },
    ssr: {
      noExternal: ['@tanstack/svelte-query', '@tanstack/query-core']
    },
    server: {
      proxy: {
        '/api': {
          target: proxyTarget,
          changeOrigin: true,
          xfwd: true,
          ws: true
        },
        '/oauth': {
          target: proxyTarget,
          changeOrigin: false,
          xfwd: true
        }
      }
    }
  }
})
