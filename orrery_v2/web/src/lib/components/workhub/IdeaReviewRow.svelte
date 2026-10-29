<script lang="ts">
  import { ArrowRight, FlaskConical, Lightbulb, ThumbsUp, UserRound } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import type { IdeaItemDTO, IdeaStatus } from '#lib/types/idea'

  interface Props {
    idea: IdeaItemDTO
    busy?: boolean
    editable?: boolean
    onVote?: (idea: IdeaItemDTO) => void
    onTransition?: (idea: IdeaItemDTO, status: IdeaStatus) => void
    onReview?: (idea: IdeaItemDTO) => void
    onConvert?: (idea: IdeaItemDTO) => void
  }

  let { idea, busy = false, editable = true, onVote, onTransition, onReview, onConvert }: Props = $props()
  const t = $derived(createTranslator($locale))
</script>

<article class="grid gap-4 border-b border-border/70 px-4 py-4 last:border-b-0 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
  <div class="min-w-0">
    <div class="flex min-w-0 items-start gap-3">
      <span class="mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-md {ideaIconClasses(idea.status)}">
        {#if idea.status === 'experiment'}
          <FlaskConical class="h-4 w-4" />
        {:else}
          <Lightbulb class="h-4 w-4" />
        {/if}
      </span>
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-2">
          <a class="font-medium text-foreground hover:text-brand" href={`/ideas/${encodeURIComponent(idea.idea_id)}`}>{idea.title}</a>
          <Badge tone={ideaStatusTone(idea.status)}>{t(`workhub.ideaStatus.${idea.status}` as TranslationKey)}</Badge>
        </div>
        {#if idea.content}
          <p class="mt-1 line-clamp-2 max-w-3xl text-sm leading-5 text-muted-foreground">{idea.content}</p>
        {/if}
        <div class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <span class="font-mono">{idea.issue_number ?? idea.idea_id}</span>
          <span>{t(`workhub.impact.${idea.impact}` as TranslationKey)}</span>
          <span>{t(`workhub.effort.${idea.effort}` as TranslationKey)}</span>
          <span class="inline-flex items-center gap-1"><UserRound class="h-3 w-3" />{idea.owner_name || idea.proposer_name || '-'}</span>
          {#each idea.tags ?? [] as tag}
            <span>#{tag}</span>
          {/each}
        </div>
      </div>
    </div>
  </div>

  <div class="flex flex-wrap items-center justify-end gap-2">
    {#if editable && !['draft', 'rejected', 'archived', 'converted'].includes(idea.status)}
      <Button
        variant={idea.viewer_has_voted ? 'secondary' : 'ghost'}
        size="sm"
        class="min-w-[4.5rem]"
        disabled={busy}
        ariaLabel={`${idea.viewer_has_voted ? t('workhub.action.unvote') : t('workhub.action.vote')}: ${idea.title}`}
        onclick={() => onVote?.(idea)}
      >
        <ThumbsUp class="h-3.5 w-3.5" />
        {idea.vote_count}
      </Button>
    {/if}

    {#if editable && (idea.status === 'draft' || idea.status === 'rejected' || idea.status === 'archived')}
      <Button variant="outline" size="sm" disabled={busy} onclick={() => onTransition?.(idea, 'submitted')}>
        {idea.status === 'archived' ? t('workhub.action.restore') : t('workhub.action.submit')}
      </Button>
    {:else if editable && idea.status === 'submitted'}
      <Button variant="outline" size="sm" disabled={busy} onclick={() => onTransition?.(idea, 'under_review')}>
        {t('workhub.action.startReview')}
        <ArrowRight class="h-3.5 w-3.5" />
      </Button>
    {:else if editable && idea.status === 'under_review'}
      <Button variant="outline" size="sm" disabled={busy} onclick={() => onReview?.(idea)}>{t('workhub.action.decide')}</Button>
    {:else if editable && (idea.status === 'accepted' || idea.status === 'experiment')}
      <Button size="sm" disabled={busy} onclick={() => onConvert?.(idea)}>
        {t('workhub.action.convert')}
        <ArrowRight class="h-3.5 w-3.5" />
      </Button>
    {:else if idea.status === 'converted' && idea.converted_todo_id}
      <Button href={`/todos/${encodeURIComponent(idea.converted_todo_id)}`} variant="outline" size="sm">
        {t('workhub.detail.convertedTask')}
        <ArrowRight class="h-3.5 w-3.5" />
      </Button>
    {/if}
  </div>
</article>

<script lang="ts" module>
  function ideaStatusTone(status: IdeaStatus): 'neutral' | 'success' | 'warning' | 'info' | 'teal' | 'violet' | 'rose' {
    switch (status) {
      case 'accepted':
      case 'converted':
        return 'success'
      case 'experiment':
        return 'teal'
      case 'under_review':
        return 'violet'
      case 'rejected':
        return 'rose'
      case 'submitted':
        return 'warning'
      case 'draft':
      case 'archived':
        return 'neutral'
      default:
        return 'info'
    }
  }

  function ideaIconClasses(status: IdeaStatus) {
    if (status === 'experiment') return 'bg-teal-50 text-teal-700 dark:bg-teal-950/40 dark:text-teal-300'
    if (status === 'accepted' || status === 'converted') return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
    if (status === 'rejected') return 'bg-rose-50 text-rose-700 dark:bg-rose-950/40 dark:text-rose-300'
    return 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300'
  }
</script>
