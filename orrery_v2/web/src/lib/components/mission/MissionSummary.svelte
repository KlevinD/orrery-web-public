<script lang="ts">
  import KpiCard from '#lib/components/shared/KpiCard.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { MissionControlViewModel } from '#lib/types/view-models'

  interface Props {
    view: MissionControlViewModel
  }

  let { view }: Props = $props()
  let t = $derived(createTranslator($locale))
</script>

<section class="grid gap-3 md:grid-cols-6">
  <KpiCard label={t('workspace.summary.activeTasks')} value={view.summary.activeTasks} />
  <KpiCard label={t('workspace.summary.approvals')} value={view.summary.pendingApprovals} />
  <KpiCard label={t('workspace.summary.risks')} value={view.summary.risks} />
  <KpiCard label={t('workspace.summary.blockers')} value={view.summary.blockers} />
  <KpiCard label={t('workspace.summary.team')} value={view.summary.teamSize} />
  <KpiCard label={t('workspace.summary.delivery')} value={`${view.summary.deliveryPercent}%`} />
</section>
