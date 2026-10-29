<script lang="ts">
  import KnowledgeProviderPanel from './KnowledgeProviderPanel.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import {
    useKnowledgeProviderCreateMutation,
    useKnowledgeProvidersQuery,
    useKnowledgeProviderSyncMutation,
    useKnowledgeProviderTestMutation
  } from '#lib/queries/knowledge-provider'
  import { useMcpServersQuery } from '#lib/queries/resources'
  import type { KnowledgeProviderRequest } from '#lib/types/api/knowledge-provider'

  let error = $state('')
  let resultMessage = $state('')
  let activeAction = $state('')

  const providersQuery = useKnowledgeProvidersQuery()
  const mcpServersQuery = useMcpServersQuery(() => ({ limit: 1000 }))
  const createProvider = useKnowledgeProviderCreateMutation()
  const testProvider = useKnowledgeProviderTestMutation()
  const syncProvider = useKnowledgeProviderSyncMutation()
  const t = $derived(createTranslator($locale))

  function create(request: KnowledgeProviderRequest) {
    beginAction('create')
    createProvider.mutate(request, {
      onSuccess: (binding) => finishAction(t('settings.knowledgeMcpCreated', { name: binding.name })),
      onError: (cause) => failAction(cause, t('settings.saveFailed'))
    })
  }

  function test(id: string) {
    beginAction(`test:${id}`)
    testProvider.mutate(id, {
      onSuccess: (result) => finishAction(t('settings.knowledgeMcpTested', { count: Object.keys(result.capabilities).length })),
      onError: (cause) => failAction(cause, t('settings.knowledgeMcpTestFailed'))
    })
  }

  function sync(id: string, ownerId: string) {
    beginAction(`sync:${id}`)
    syncProvider.mutate({ id, ownerId }, {
      onSuccess: (result) => finishAction(t('settings.knowledgeMcpSynced', { folders: result.folders_synced, documents: result.documents_synced })),
      onError: (cause) => failAction(cause, t('settings.knowledgeMcpSyncFailed'))
    })
  }

  function beginAction(action: string) {
    error = ''
    resultMessage = ''
    activeAction = action
  }

  function finishAction(message: string) {
    activeAction = ''
    resultMessage = message
  }

  function failAction(cause: unknown, fallback: string) {
    activeAction = ''
    error = cause instanceof Error ? cause.message : fallback
  }
</script>

<KnowledgeProviderPanel
  providers={providersQuery.data?.items ?? []}
  servers={mcpServersQuery.data?.servers ?? []}
  loading={providersQuery.isLoading || mcpServersQuery.isLoading}
  busy={createProvider.isPending || testProvider.isPending || syncProvider.isPending}
  {activeAction}
  {error}
  {resultMessage}
  oncreate={create}
  ontest={test}
  onsync={sync}
/>
