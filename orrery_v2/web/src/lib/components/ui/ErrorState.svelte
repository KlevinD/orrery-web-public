<script lang="ts">
  import { AlertTriangle } from 'lucide-svelte'
  import PermissionDeniedState from './PermissionDeniedState.svelte'
  import { isPermissionDeniedMessage } from '#lib/permissions/access'

  interface Props {
    title?: string
    message?: string
    correlationId?: string
  }

  let {
    title = 'Request failed',
    message = 'The backend could not complete this request.',
    correlationId
  }: Props = $props()

  let permissionDenied = $derived(isPermissionDeniedMessage(message))
</script>

{#if permissionDenied}
  <PermissionDeniedState />
{:else}
  <div class="rounded-xl border border-destructive/30 bg-destructive/10 p-5 text-sm">
    <div class="flex items-center gap-2 font-medium text-destructive-foreground">
      <AlertTriangle class="h-4 w-4" />
      {title}
    </div>
    <div class="mt-2 text-destructive-foreground/80">{message}</div>
    {#if correlationId}
      <div class="mt-3 inline-flex rounded border border-destructive/30 px-2 py-1 font-mono text-xs text-destructive-foreground/70">
        {correlationId}
      </div>
    {/if}
  </div>
{/if}
