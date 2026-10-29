export const prerender = false

export function load({ params }) {
  return { toolId: params.toolId }
}
