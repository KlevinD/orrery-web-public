<script lang="ts">
  import { createTranslator, locale } from '#lib/i18n'

  interface Props {
    evidenceCount: number
    qaCount: number
  }

  let { evidenceCount, qaCount }: Props = $props()
  let t = $derived(createTranslator($locale))
</script>

<section class="panel grid gap-3 p-4 md:grid-cols-2">
  <div>
    <div class="text-xs uppercase tracking-wide text-muted-foreground">{t('workspace.evidence')}</div>
    <div class="mt-2 text-xl font-semibold text-foreground">{evidenceCount}</div>
  </div>
  <div>
    <div class="text-xs uppercase tracking-wide text-muted-foreground">{t('workspace.qaChecks')}</div>
    <div class="mt-2 text-xl font-semibold text-foreground">{qaCount}</div>
  </div>
</section>
