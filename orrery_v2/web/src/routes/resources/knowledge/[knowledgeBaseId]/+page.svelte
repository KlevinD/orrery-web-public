<script lang="ts">
  import KnowledgeWorkspace from '#lib/components/knowledge/KnowledgeWorkspace.svelte'

  interface Props {
    data: { knowledgeBaseId: string }
  }

  let { data }: Props = $props()
</script>

<svelte:head>
  <title>知识工作台 | Orrery</title>
</svelte:head>

<KnowledgeWorkspace knowledgeBaseId={data.knowledgeBaseId} />
