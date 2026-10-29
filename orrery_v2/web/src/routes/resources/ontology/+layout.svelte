<script lang="ts">
  import OntologyWorkspaceShell from '#lib/components/ontology/OntologyWorkspaceShell.svelte'
  let { children }: { children: import('svelte').Snippet } = $props()
</script>

<OntologyWorkspaceShell>{@render children()}</OntologyWorkspaceShell>
