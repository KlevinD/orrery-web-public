export const prerender = false

export function load({ params, url }) {
  return {
    skillId: params.skillId,
    mode: url.searchParams.get('mode') ?? (params.skillId === 'new' ? 'edit' : '')
  }
}
