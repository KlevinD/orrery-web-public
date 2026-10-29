<script lang="ts">
  import IssueDetailPage from '#lib/components/issues/IssueDetailPage.svelte'

  interface Props {
    data: { todoId: string }
  }

  let { data }: Props = $props()
</script>

<IssueDetailPage kind="todo" issueId={data.todoId} />
