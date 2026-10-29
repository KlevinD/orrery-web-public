<script lang="ts">
  import IssueDetailPage from '#lib/components/issues/IssueDetailPage.svelte'

  interface Props {
    data: { ideaId: string }
  }

  let { data }: Props = $props()
</script>

<IssueDetailPage kind="idea" issueId={data.ideaId} />
