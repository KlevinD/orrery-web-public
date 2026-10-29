<script lang="ts">
  import { X } from 'lucide-svelte'
  import type { Snippet } from 'svelte'
  import { modalFocus } from '#lib/actions/modalFocus'

  interface Props {
    open?: boolean
    title?: string
    size?: 'default' | 'wide'
    children: Snippet
    onClose?: () => void
    class?: string
  }

  let {
    open = false,
    title = '',
    size = 'default',
    children,
    onClose,
    class: className = ''
  }: Props = $props()

  const sizeClass = $derived(
    size === 'wide' ? 'max-w-5xl max-h-[90vh] overflow-y-auto' : 'max-w-lg max-h-[90vh] overflow-y-auto'
  )

  function handleOverlayClick() {
    onClose?.()
  }

</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 p-4 backdrop-blur-[2px] transition-opacity"
    onclick={handleOverlayClick}
    data-dialog-overlay
    role="presentation"
  >
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      role="dialog"
      tabindex="-1"
      aria-modal="true"
      aria-label={title || undefined}
      class="relative grid min-w-0 w-full gap-4 rounded-lg border border-border bg-card p-5 shadow-lg animate-in fade-in-0 zoom-in-95 sm:p-6 {sizeClass} {className}"
      onclick={(e) => e.stopPropagation()}
      use:modalFocus={onClose}
    >
      {#if title}
        <div class="min-w-0 pr-6 text-center sm:text-left">
          <h2 class="break-words text-lg font-semibold leading-snug tracking-normal">{title}</h2>
        </div>
      {/if}
      <div class="min-w-0">
        {@render children()}
      </div>
      {#if onClose}
        <button
          type="button"
          class="absolute right-4 top-4 rounded-sm opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2"
          onclick={onClose}
          aria-label="Close"
        >
          <X class="h-4 w-4" />
        </button>
      {/if}
    </div>
  </div>
{/if}
