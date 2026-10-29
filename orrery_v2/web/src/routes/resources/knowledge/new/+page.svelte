<script lang="ts">
  import { goto } from '$app/navigation'
  import { ArrowLeft, Database } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import { locale, createTranslator } from '#lib/i18n'
  import { createKnowledgeBase } from '#lib/queries/knowledge'

  const t = $derived(createTranslator($locale))

  let creating = $state(false)
  let error = $state('')
  let newBaseName = $state('')
  let newBaseDescription = $state('')
  let newOwnerType = $state('global')
  let newVisibility = $state('public')
  let newProvider = $state('pageindex')

  async function createBase() {
    const name = newBaseName.trim()
    if (!name) {
      return
    }
    creating = true
    error = ''
    try {
      const base = await createKnowledgeBase({
        name,
        description: newBaseDescription.trim(),
        owner_type: newOwnerType,
        owner_id: '',
        visibility: newVisibility,
        provider: newProvider
      })
      goto(`/resources/knowledge/${encodeURIComponent(base.id)}`)
    } catch (err) {
      error = errorMessage(err)
    } finally {
      creating = false
    }
  }

  function errorMessage(err: unknown) {
    if (err instanceof Error) {
      return err.message
    }
    return String(err)
  }
</script>

<svelte:head>
  <title>{t('resources.knowledge.createBase')} · {t('nav.knowledge')} · Orrery</title>
</svelte:head>

<section class="page-shell max-w-3xl space-y-5">
  <header class="flex flex-col gap-3 border-b border-border pb-4">
    <div>
      <Button href="/resources/knowledge" variant="ghost" size="sm" class="mb-2">
        <ArrowLeft class="h-3.5 w-3.5" />
        {t('resources.knowledge.backToList')}
      </Button>
      <h1 class="page-title">{t('resources.knowledge.createBase')}</h1>
      <p class="page-description">{t('resources.knowledge.createDescription')}</p>
    </div>
  </header>

  {#if error}
    <ErrorState title={t('resources.knowledge.requestFailed')} message={error} />
  {/if}

  <section class="rounded-xl border border-border bg-card p-5">
    <div class="grid gap-4 md:grid-cols-2">
      <label class="block space-y-1.5 md:col-span-2">
        <span class="text-xs font-medium text-muted-foreground">{t('resources.knowledge.baseName')}</span>
        <Input bind:value={newBaseName} />
      </label>
      <label class="block space-y-1.5 md:col-span-2">
        <span class="text-xs font-medium text-muted-foreground">{t('resources.knowledge.description')}</span>
        <Input bind:value={newBaseDescription} />
      </label>
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('resources.knowledge.ownerType')}</span>
        <select
          bind:value={newOwnerType}
          class="h-9 w-full rounded-md border border-border bg-input-background px-3 text-sm text-foreground shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          <option value="global">{t('resources.knowledge.ownerType.global')}</option>
          <option value="organization">{t('resources.knowledge.ownerType.organization')}</option>
          <option value="position">{t('resources.knowledge.ownerType.position')}</option>
          <option value="agent">{t('resources.knowledge.ownerType.agent')}</option>
          <option value="human">{t('resources.knowledge.ownerType.human')}</option>
        </select>
      </label>
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('resources.knowledge.visibility')}</span>
        <select
          bind:value={newVisibility}
          class="h-9 w-full rounded-md border border-border bg-input-background px-3 text-sm text-foreground shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          <option value="private">{t('resources.knowledge.visibility.personal')}</option>
          <option value="public">{t('resources.knowledge.visibility.public')}</option>
        </select>
      </label>
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('resources.knowledge.provider')}</span>
        <select
          bind:value={newProvider}
          class="h-9 w-full rounded-md border border-border bg-input-background px-3 text-sm text-foreground shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          <option value="pageindex">{t('resources.knowledge.provider.pageindex')}</option>
          <option value="local">{t('resources.knowledge.provider.local')}</option>
        </select>
      </label>
    </div>

    <div class="mt-5 flex justify-end">
      <Button onclick={createBase} disabled={creating || newBaseName.trim() === ''}>
        <Database class="h-4 w-4" />
        {t('resources.knowledge.createBase')}
      </Button>
    </div>
  </section>
</section>
