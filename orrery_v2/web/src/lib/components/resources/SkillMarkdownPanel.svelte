<script lang="ts">
  import { marked } from 'marked'
  import { Code2, Columns2, Eye, Save } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale } from '#lib/i18n'

  interface Props {
    raw: string
    editable?: boolean
    busy?: boolean
    saveEnabled?: boolean
    onsave?: (raw: string) => void
  }

  type MarkdownMode = 'preview' | 'source' | 'compare'

  let { raw, editable = false, busy = false, saveEnabled = false, onsave }: Props = $props()
  let mode = $state<MarkdownMode>('preview')
  let draft = $state('')
  let t = $derived(createTranslator($locale))
  const rendered = $derived(renderMarkdown(draft))
  const sourceReadonly = $derived(!editable || busy)
  const hasChanges = $derived(draft !== raw)
  const saveBlocked = $derived(busy || (!hasChanges && !saveEnabled))

  $effect(() => {
    draft = raw
  })

  function renderMarkdown(value: string) {
    const html = marked.parse(escapeHtml(value || t('resources.skills.emptySkillMd')), {
      async: false,
      breaks: true,
      gfm: true
    }) as string
    return sanitizeRenderedHtml(html)
  }

  function escapeHtml(value: string) {
    return value
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;')
  }

  function save() {
    if (!editable || saveBlocked) return
    onsave?.(draft)
  }

  function sanitizeRenderedHtml(html: string) {
    if (typeof DOMParser === 'undefined') return html
    const document = new DOMParser().parseFromString(html, 'text/html')
    for (const element of document.querySelectorAll('a[href], img[src]')) {
      const attribute = element instanceof HTMLAnchorElement ? 'href' : 'src'
      const value = element.getAttribute(attribute)
      if (value && !isAllowedUrl(value)) {
        element.removeAttribute(attribute)
      }
    }
    return document.body.innerHTML
  }

  function isAllowedUrl(value: string) {
    const trimmed = value.trim()
    if (trimmed.startsWith('/') || trimmed.startsWith('./') || trimmed.startsWith('../') || trimmed.startsWith('#')) return true
    try {
      const url = new URL(trimmed)
      return ['http:', 'https:', 'mailto:', 'tel:'].includes(url.protocol)
    } catch {
      return false
    }
  }
</script>

<section class="flex min-h-[32rem] flex-col overflow-hidden rounded-xl border border-border bg-card">
  <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
    <div>
      <h2 class="text-sm font-semibold text-foreground">{t('resources.skills.skillMd')}</h2>
      <p class="text-xs text-muted-foreground">{editable ? t('resources.skills.markdownEditable') : t('resources.skills.markdownReadonly')}</p>
    </div>
    <div class="flex flex-wrap items-center gap-1">
      <Button variant={mode === 'preview' ? 'secondary' : 'ghost'} size="sm" onclick={() => (mode = 'preview')} ariaLabel={t('resources.skills.previewMode')}>
        <Eye class="h-3.5 w-3.5" />
        {t('resources.skills.preview')}
      </Button>
      <Button variant={mode === 'source' ? 'secondary' : 'ghost'} size="sm" onclick={() => (mode = 'source')} ariaLabel={t('resources.skills.sourceMode')}>
        <Code2 class="h-3.5 w-3.5" />
        {t('resources.skills.source')}
      </Button>
      <Button variant={mode === 'compare' ? 'secondary' : 'ghost'} size="sm" onclick={() => (mode = 'compare')} ariaLabel={t('resources.skills.compareMode')}>
        <Columns2 class="h-3.5 w-3.5" />
        {t('resources.skills.compare')}
      </Button>
      {#if editable}
        <Button size="sm" disabled={saveBlocked} onclick={save} ariaLabel={t('resources.skills.saveSkillMd')}>
          <Save class="h-3.5 w-3.5" />
          {t('resources.skills.saveSkillMd')}
        </Button>
      {/if}
    </div>
  </div>

  {#if mode === 'compare'}
    <div class="grid min-h-0 flex-1 gap-0 lg:grid-cols-2">
      <textarea
        aria-label="SKILL.md source"
        class="min-h-[32rem] resize-none border-0 border-r border-border bg-background p-4 font-mono text-sm leading-6 text-foreground outline-none disabled:bg-muted/25"
        value={draft}
        disabled={sourceReadonly}
        oninput={(event) => (draft = event.currentTarget.value)}
      ></textarea>
      <article class="skill-markdown-preview min-h-[32rem] overflow-auto p-5 text-sm leading-7 text-foreground">
        {@html rendered}
      </article>
    </div>
  {:else if mode === 'source'}
    <textarea
      aria-label="SKILL.md source"
      class="min-h-[32rem] flex-1 resize-none border-0 bg-background p-4 font-mono text-sm leading-6 text-foreground outline-none disabled:bg-muted/25"
      value={draft}
      disabled={sourceReadonly}
      oninput={(event) => (draft = event.currentTarget.value)}
    ></textarea>
  {:else}
    <article class="skill-markdown-preview min-h-[32rem] flex-1 overflow-auto p-5 text-sm leading-7 text-foreground">
      {@html rendered}
    </article>
  {/if}
</section>
