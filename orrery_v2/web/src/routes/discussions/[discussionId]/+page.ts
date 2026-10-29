export const prerender = false

export function load({ params }) {
  return { discussionId: params.discussionId }
}
