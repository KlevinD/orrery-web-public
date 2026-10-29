export const prerender = false

export function load({ params }) {
  return { missionId: params.missionId }
}
