<script lang="ts">
  import { onMount } from 'svelte'
  import { createTranslator, locale } from '#lib/i18n'
  const t = $derived(createTranslator($locale))
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import { legacyApprovalHref } from '#lib/components/workhub/work-actions'
  const target = $derived(legacyApprovalHref($page.url))
  onMount(() => { void goto(target, { replaceState: true }) })
</script>

<a class="m-6 inline-block text-brand underline" href={target}>{t('nav.workItems')}</a>
