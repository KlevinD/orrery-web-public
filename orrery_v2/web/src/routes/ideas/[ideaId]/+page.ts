export const prerender = false

export function load({ params }) {
  return { ideaId: params.ideaId }
}
