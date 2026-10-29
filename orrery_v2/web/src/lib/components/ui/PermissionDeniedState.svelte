<script lang="ts">
  import { ShieldX } from 'lucide-svelte'
  import { createTranslator, locale } from '#lib/i18n'

  let t = $derived(createTranslator($locale))
</script>

<section class="flex min-h-[18rem] items-center justify-center py-8" aria-labelledby="permission-denied-title">
  <div class="w-full max-w-lg rounded-xl border border-border bg-card p-6 text-center">
    <div class="mx-auto flex h-10 w-10 items-center justify-center rounded-md bg-muted text-muted-foreground">
      <ShieldX class="h-5 w-5" />
    </div>
    <h1 id="permission-denied-title" class="mt-4 text-lg font-semibold text-foreground">
      {t('state.permissionDenied')}
    </h1>
    <p class="mx-auto mt-2 max-w-md text-sm leading-6 text-muted-foreground">
      {t('state.permissionDeniedDescription')}
    </p>
  </div>
</section>
