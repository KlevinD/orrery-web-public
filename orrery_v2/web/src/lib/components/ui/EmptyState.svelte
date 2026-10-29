<script lang="ts">
  import { Inbox } from 'lucide-svelte'

  interface Props {
    title?: string
    description?: string
  }

  let { title = 'Nothing here yet', description = 'The backend returned an empty collection.' }: Props = $props()
</script>

<div class="panel flex min-h-40 flex-col items-center justify-center p-8 text-center">
  <Inbox class="h-8 w-8 text-muted-foreground" />
  <div class="mt-3 text-sm font-medium text-foreground">{title}</div>
  <div class="mt-1 max-w-md text-xs leading-5 text-muted-foreground">{description}</div>
</div>
