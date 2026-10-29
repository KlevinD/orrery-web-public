export const prerender = false

export function load({ params }) {
  return {
    knowledgeBaseId: params.knowledgeBaseId,
    documentId: params.documentId
  }
}
