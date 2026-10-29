import { marked, type Token, type Tokens } from 'marked'

export function renderSafeMarkdown(raw: string, resolveUrl?: (url: string) => string) {
  const html = marked.parse(escapeHtml(raw || ''), {
    async: false,
    breaks: true,
    gfm: true,
    walkTokens: (token) => sanitizeMarkdownToken(token, resolveUrl)
  }) as string
  return sanitizeRenderedHtml(html)
}

function sanitizeMarkdownToken(token: Token, resolveUrl?: (url: string) => string) {
  if (token.type !== 'link' && token.type !== 'image') return
  const linkedToken = token as Tokens.Link | Tokens.Image
  if (resolveUrl) linkedToken.href = resolveUrl(linkedToken.href)
  if (!isAllowedUrl(linkedToken.href)) {
    linkedToken.href = ''
  }
}

function escapeHtml(raw: string) {
  return raw
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

function sanitizeRenderedHtml(html: string) {
  if (typeof DOMParser === 'undefined') return html.replace(/\s(?:href|src)=""/g, '')
  const document = new DOMParser().parseFromString(html, 'text/html')
  for (const element of document.querySelectorAll('a[href], img[src]')) {
    const attribute = element instanceof HTMLAnchorElement ? 'href' : 'src'
    const currentValue = element.getAttribute(attribute)
    if (currentValue !== null && !isAllowedUrl(currentValue)) {
      element.removeAttribute(attribute)
    }
    if (element instanceof HTMLAnchorElement && currentValue?.includes('/attachments/') && currentValue.includes('/preview')) {
      element.target = '_blank'
      element.rel = 'noopener noreferrer'
    }
  }
  return document.body.innerHTML
}

function isAllowedUrl(raw: string) {
  const trimmed = raw.trim()
  if (trimmed.startsWith('/') || trimmed.startsWith('./') || trimmed.startsWith('../') || trimmed.startsWith('#')) return true
  try {
    const url = new URL(trimmed)
    return ['http:', 'https:', 'mailto:', 'tel:'].includes(url.protocol)
  } catch {
    return false
  }
}
