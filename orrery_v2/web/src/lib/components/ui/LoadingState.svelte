<script lang="ts">
  interface Props {
    title?: string
    description?: string
  }

  let { title = 'Loading', description = 'Fetching the latest operational data.' }: Props = $props()
</script>

<div class="panel flex min-h-32 items-center gap-4 p-5 text-sm text-muted-foreground">
  <div class="h-5 w-5 animate-spin rounded-full border-2 border-brand border-t-transparent"></div>
  <div>
    <div class="font-medium text-foreground">{title}</div>
    <div class="mt-1 text-xs text-muted-foreground">{description}</div>
  </div>
</div>
