<script lang="ts">
  let { showClose = false, onClose }: { showClose?: boolean; onClose?: () => void } = $props()
</script>

<aside data-testid="loaded-context-panel">
  {#if showClose}
    <button onclick={onClose}>Close loaded panel</button>
  {/if}
</aside>
