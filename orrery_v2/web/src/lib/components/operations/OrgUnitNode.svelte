<script lang="ts">
  import { Briefcase, Plus, Users } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import OrgUnitNode from './OrgUnitNode.svelte'
  import type { OrganizationUnitViewModel } from '#lib/types/view-models'

  interface Props {
    unit: OrganizationUnitViewModel
    depth?: number
    onaddunit?: (organizationId: string, parentDepartmentId: string) => void
  }

  let { unit, depth = 0, onaddunit }: Props = $props()
  let t = $derived(createTranslator($locale))

  function addUnit() {
    onaddunit?.(unit.organizationId, unit.id)
  }
</script>

<article class="border-l border-border pl-4" style={`margin-left: ${depth * 14}px`}>
  <div class="rounded-xl border border-border bg-card/60 p-3">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-2">
          <h3 class="truncate text-sm font-semibold text-foreground">{unit.name}</h3>
          <Badge>{t('organization.unit')}</Badge>
        </div>
        <p class="mt-1 text-xs text-muted-foreground">
          {t('organization.memberAffiliation', { count: unit.memberCount })}
        </p>
      </div>
      <Button size="sm" variant="outline" onclick={addUnit}>
        <Plus class="mr-1.5 h-3.5 w-3.5" />
        {t('organization.addUnit')}
      </Button>
    </div>

    {#if unit.positions.length > 0}
      <div class="mt-3 space-y-2">
        {#each unit.positions as position (position.id)}
          <div class="rounded-md bg-muted/45 p-2">
            <div class="flex items-center gap-2 text-xs font-medium text-foreground">
              <Briefcase class="h-3.5 w-3.5 text-muted-foreground" />
              <span>{position.name}</span>
              <span class="text-muted-foreground">{t('workspace.teamCount', { count: position.members.length })}</span>
            </div>
            {#if position.members.length > 0}
              <div class="mt-2 flex flex-wrap gap-1.5">
                {#each position.members as member (member.profileId)}
                  <Badge tone={member.enabled ? 'success' : 'danger'}>
                    <Users class="mr-1 h-3 w-3" />
                    {member.displayName}
                  </Badge>
                {/each}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>

  {#if unit.children.length > 0}
    <div class="mt-3 space-y-3">
      {#each unit.children as child (child.id)}
        <OrgUnitNode unit={child} depth={depth + 1} {onaddunit} />
      {/each}
    </div>
  {/if}
</article>
