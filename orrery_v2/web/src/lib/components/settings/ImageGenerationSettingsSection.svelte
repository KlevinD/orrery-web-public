<script lang="ts">
  import ImageGenerationSettingsPanel from './ImageGenerationSettingsPanel.svelte'
  import { useImageGenerationSettingsQuery, useImageGenerationSettingsMutation, useImageGenerationConnectionTestMutation } from '#lib/queries/image-generation'
  import type { ImageGenerationSettingsDTO } from '#lib/types/api/image-generation'

  let { canManage = false }: { canManage?: boolean } = $props()
  const query = useImageGenerationSettingsQuery()
  const update = useImageGenerationSettingsMutation()
  const check = useImageGenerationConnectionTestMutation()

  async function save(settings: ImageGenerationSettingsDTO) {
    if (!canManage) throw new Error('Permission denied')
    return update.mutateAsync(settings)
  }

  async function test() {
    if (!canManage) throw new Error('Permission denied')
    return check.mutateAsync()
  }
</script>

<ImageGenerationSettingsPanel
  settings={query.data}
  loading={query.isLoading}
  loadFailed={query.isError}
  readOnly={!canManage}
  busy={update.isPending || check.isPending}
  onsave={save}
  ontest={test}
  onretry={() => query.refetch()}
/>
