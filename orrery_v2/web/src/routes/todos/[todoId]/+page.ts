export const prerender = false

export function load({ params }) {
  return { todoId: params.todoId }
}
