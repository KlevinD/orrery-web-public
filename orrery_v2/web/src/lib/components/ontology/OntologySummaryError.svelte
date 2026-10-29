<script lang="ts">
  import { RefreshCw } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  interface Props { message: string; onretry: () => void }
  let { message, onretry }: Props = $props()
</script>

<div class="mt-4 border-l-2 border-destructive pl-3 text-sm" role="alert">
  <p class="text-destructive">{message}</p>
  <Button class="mt-2" size="sm" variant="ghost" onclick={onretry}><RefreshCw class="size-3.5" aria-hidden="true" />重试</Button>
</div>
