<script lang="ts">
  import QueryProvider from '#lib/components/providers/QueryProvider.svelte'
  import DiscussionWorkspace from './DiscussionWorkspace.svelte'

  interface Props {
    initialDiscussionId?: string
    initialDraft?: string
    initialSendImmediately?: boolean
    initialDirectTargetId?: string
    initialDirectTargetKind?: 'agent' | 'user'
    initialDirectTargetName?: string
    initialAssistant?: boolean
  }

  let {
    initialDiscussionId = '',
    initialDraft = '',
    initialSendImmediately = false,
    initialDirectTargetId = '',
    initialDirectTargetKind,
    initialDirectTargetName = '',
    initialAssistant = false
  }: Props = $props()
</script>

<QueryProvider>
  <DiscussionWorkspace
    {initialDiscussionId}
    {initialDraft}
    {initialSendImmediately}
    {initialDirectTargetId}
    {initialDirectTargetKind}
    {initialDirectTargetName}
    {initialAssistant}
  />
</QueryProvider>
