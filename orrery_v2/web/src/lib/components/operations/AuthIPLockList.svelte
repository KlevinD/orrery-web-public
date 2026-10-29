<script lang="ts">
  import { UnlockKeyhole } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import type { AuthIPLockCenterViewModel } from '#lib/types/view-models'

  interface Props {
    view: AuthIPLockCenterViewModel
    busyIP?: string
    onUnlock: (ipAddress: string) => void
  }

  let { view, busyIP = '', onUnlock }: Props = $props()
</script>

{#if view.locks.length === 0}
  <EmptyState title="暂无登录 IP 锁定" description="当前没有需要管理员处理的锁定 IP。" />
{:else}
  <div class="grid gap-3">
    {#each view.locks as lock (lock.ipAddress)}
      <article class="panel p-4">
        <div class="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <Badge tone={lock.statusTone}>{lock.status}</Badge>
              <h2 class="truncate text-sm font-semibold text-foreground">{lock.ipAddress}</h2>
              <span class="text-xs text-muted-foreground">{lock.lockedAt}</span>
            </div>
            <div class="mt-3 grid gap-3 text-xs text-muted-foreground sm:grid-cols-2 lg:grid-cols-4">
              <div>
                <span>失败次数</span>
                <div class="mt-1 text-foreground">{lock.failureCount}</div>
              </div>
              <div>
                <span>最后用户名</span>
                <div class="mt-1 truncate text-foreground">{lock.lastUsername}</div>
              </div>
              <div>
                <span>最后原因</span>
                <div class="mt-1 truncate text-foreground">{lock.lastReason}</div>
              </div>
              <div>
                <span>更新时间</span>
                <div class="mt-1 text-foreground">{lock.updatedAt}</div>
              </div>
            </div>
          </div>

          <Button
            variant="outline"
            size="sm"
            disabled={busyIP === lock.ipAddress}
            onclick={() => onUnlock(lock.ipAddress)}
          >
            <UnlockKeyhole class="h-4 w-4" />
            <span>{busyIP === lock.ipAddress ? '解除中' : '解除锁定'}</span>
          </Button>
        </div>
      </article>
    {/each}
  </div>
{/if}
