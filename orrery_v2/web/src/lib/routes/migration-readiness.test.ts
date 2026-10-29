import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const sourceRoots = ['src/routes', 'src/lib']

function collectFiles(dir: string): string[] {
  return readdirSync(dir)
    .flatMap((entry) => {
      const path = join(dir, entry)
      return statSync(path).isDirectory() ? collectFiles(path) : [path]
    })
}

describe('migration readiness', () => {
  it('does not keep stage-one placeholder components in the migrated frontend', () => {
    expect(existsSync('src/lib/components/ui/PagePlaceholder.svelte')).toBe(false)
  })

  it('does not ship placeholder copy that contradicts full migration acceptance', () => {
    const sources = sourceRoots
      .flatMap(collectFiles)
      .filter((file) => /\.(svelte|ts)$/.test(file) && !file.endsWith('.test.ts'))

    for (const file of sources) {
      const source = readFileSync(file, 'utf-8')
      expect(source, file).not.toMatch(/Go API wired in later stages|Stage 1 acceptance|Ready for real API integration/i)
    }
  })

  it('does not expose migration stage metadata in navigation', () => {
    const source = readFileSync('src/lib/routes/navigation.ts', 'utf-8')

    expect(source).not.toMatch(/\bstage:/)
    expect(source).not.toMatch(/Stage \d/i)
  })

  it('requires migrated governance routes to use the shared i18n translator', () => {
    for (const file of ['src/routes/approvals/+page.svelte']) {
      const source = readFileSync(file, 'utf-8')

      expect(source, file).toContain("createTranslator($locale)")
      expect(source, file).toContain("t('nav.")
      expect(source, file).not.toMatch(/<title>[A-Z][^<{|]*\| Orrery<\/title>/)
      expect(source, file).not.toMatch(/<h1[^>]*>[A-Z][^<{]*<\/h1>/)
    }
  })

  it('requires migrated shared widgets to use i18n for visible chrome copy', () => {
    for (const file of [
      'src/lib/components/approvals/ApprovalList.svelte',
      'src/lib/components/mission/MissionSummary.svelte',
      'src/lib/components/mission/MissionTaskBoard.svelte',
      'src/lib/components/mission/MissionEvidenceList.svelte'
    ]) {
      const source = readFileSync(file, 'utf-8')

      expect(source, file).toContain("createTranslator($locale)")
      expect(source, file).not.toMatch(/title="No [^"]+"/)
      expect(source, file).not.toMatch(/description="[A-Z][^"]+"/)
      expect(source, file).not.toMatch(/label="[a-z][^"]+"/)
      expect(source, file).not.toMatch(/<span class="text-slate-500">[A-Z][^<]+<\/span>/)
    }
  })

  it('exposes an immediate Chinese and English language switch in the app chrome', () => {
    const source = readFileSync('src/lib/components/layout/TopBar.svelte', 'utf-8')

    expect(source).toContain("setLocale((event.currentTarget as HTMLSelectElement).value as Locale)")
    expect(source).toContain('id="language-select"')
    expect(source).toContain('value="zh-CN"')
    expect(source).toContain('value="en-US"')
    expect(source).toContain("t('topbar.language')")
  })

  it('keeps mission list card counts aligned with the React demo and i18n', () => {
    const source = readFileSync('src/lib/components/mission/MissionList.svelte', 'utf-8')

    expect(source).toContain("t('workspace.teamCount'")
    expect(source).not.toContain('{mission.teamSize}</span>')
  })

  it('keeps squad and evidence chrome labels translated', () => {
    const squadSource = readFileSync('src/routes/squads/+page.svelte', 'utf-8')
    const evidenceSource = readFileSync('src/lib/components/mission/MissionEvidenceList.svelte', 'utf-8')

    expect(squadSource).toContain("t('squads.mission')")
    expect(squadSource).not.toContain('Mission: {squad.missionId}')
    expect(evidenceSource).toContain("t('workspace.evidence')")
    expect(evidenceSource).toContain("t('workspace.qaChecks')")
    expect(evidenceSource).not.toContain('>Evidence<')
    expect(evidenceSource).not.toContain('>QA Checks<')
  })

  it('keeps the mission workspace visually aligned with the React demo shell', () => {
    const source = readFileSync('src/routes/missions/[missionId]/+page.svelte', 'utf-8')
    const buttonSource = readFileSync('src/lib/components/ui/Button.svelte', 'utf-8')

    expect(existsSync('src/lib/components/shared/ProfileCard.svelte')).toBe(true)
    expect(existsSync('src/lib/components/shared/ActivityFeed.svelte')).toBe(true)
    expect(source).toContain("createTranslator($locale)")
    expect(source).toContain("import ProfileCard from '#lib/components/shared/ProfileCard.svelte'")
    expect(source).toContain("import ActivityFeed from '#lib/components/shared/ActivityFeed.svelte'")
    expect(source).toContain('useMissionControlQuery(() => resolvedMissionId)')
    expect(source).toContain('const view = $derived(controlQuery.data)')
    expect(source).toContain('const missionId = resolvedMissionId')
    expect(source).toContain('subscribeMissionStream(missionId, () => Promise.all([')
    expect(source).toContain('missionQueryKeys.team(missionId)')
    expect(source).toContain('missionQueryKeys.tasks(missionId)')
    expect(source).toContain('missionQueryKeys.progress(missionId)')
    expect(source).toContain('missionQueryKeys.evidence(missionId)')
    expect(source).toContain('missionQueryKeys.qa(missionId)')
    expect(source).toContain('view?.events.map')
    expect(source).toContain('view.progressFocus')
    expect(source).toContain("'workspace.tab.overview'")
    expect(source).toContain("'workspace.tab.timeline'")
    expect(source).toContain('workspace.controlVersion')
    expect(source).toContain('workspace.activeTasks')
    expect(source).toContain('workspace.pendingApprovals')
    expect(source).toContain('workspace.teamCount')
    expect(source).toContain('workspace.createTask')
    expect(source).toContain('workspace.assignTask')
    expect(source).toContain('workspace.submitResult')
    expect(source).toContain('workspace.approveQa')
    expect(source).toContain('workspace.rejectQa')
    expect(source).toContain('workspace.operations')
    expect(source).toContain('showOperations')
    expect(source).toContain('workspace.toggleOperations')
    expect(source).toContain('xl:grid-cols-[1.1fr_1fr_1fr]')
    expect(source).toContain('class="ml-auto"')
    expect(source).toContain('variant="outline"')
    expect(source).toContain('size="sm"')
    expect(buttonSource).toContain("variant?: 'default' | 'destructive' | 'outline' | 'secondary' | 'ghost' | 'link'")
    expect(buttonSource).toContain("size?: 'default' | 'sm' | 'lg' | 'icon'")
    expect(source).not.toContain('bind:value={taskGoal')
    expect(source).not.toContain('controlQuery.data?.events.map')
  })

  it('keeps mission task priority labels aligned with the React demo and i18n', () => {
    const source = readFileSync('src/routes/missions/[missionId]/+page.svelte', 'utf-8')

    expect(source).toContain('workspace.priority.critical')
    expect(source).toContain('workspace.priority.high')
    expect(source).not.toContain("tx(`risk.${task.priority}`)")
  })

  it('keeps mission task cards showing backend descriptions like the React demo', () => {
    const source = readFileSync('src/routes/missions/[missionId]/+page.svelte', 'utf-8')
    const typeSource = readFileSync('src/lib/types/view-models/mission.ts', 'utf-8')

    expect(typeSource).toContain('description: string')
    expect(source).toContain('{task.description}')
    expect(source).toContain("displayField(task.assigneeName).slice(0, 2)")
    expect(source).toContain('formatList(task.deliverables)')
    expect(source).toContain('formatList(task.evidenceRequirements)')
    expect(source).toContain("t('workspace.qaRef')")
    expect(source).not.toContain('<p class="mt-1 text-xs text-slate-500">{task.goal}</p>')
  })

  it('does not keep the mission workspace pause placeholder from early migration', () => {
    const source = readFileSync('src/routes/missions/[missionId]/+page.svelte', 'utf-8')
    const i18nSource = readFileSync('src/lib/i18n.ts', 'utf-8')
    const i18nTestSource = readFileSync('src/lib/i18n.test.ts', 'utf-8')

    expect(source).not.toContain('<Button disabled>')
    expect(source).not.toContain('Pause,')
    expect(source).not.toContain("t('workspace.pauseMission')")
    expect(i18nSource).not.toContain("'workspace.pauseMission'")
    expect(i18nTestSource).not.toContain("'workspace.pauseMission'")
  })

  it('uses shared UI select controls in mission workspace operations', () => {
    const source = readFileSync('src/routes/missions/[missionId]/+page.svelte', 'utf-8')

    expect(source).toContain("import Select from '#lib/components/ui/Select.svelte'")
    expect(source).toContain("selectField('workspace.priority', taskPriority")
    expect(source).toContain("selectField('workspace.task', selectedTaskId")
    expect(source).toContain('<Select value={value}')
    expect(source).not.toContain('<select bind:value=')
  })

  it('does not expose backend implementation notes in mission workspace evidence copy', () => {
    const source = readFileSync('src/routes/missions/[missionId]/+page.svelte', 'utf-8')
    const i18nSource = readFileSync('src/lib/i18n.ts', 'utf-8')
    const i18nTestSource = readFileSync('src/lib/i18n.test.ts', 'utf-8')

    expect(source).not.toContain('detailsComingFromBackend')
    expect(source).toContain("task.qaMessage || t('workspace.notProvided')")
    expect(i18nSource).not.toContain("'workspace.detailsComingFromBackend'")
    expect(i18nTestSource).not.toContain("'workspace.detailsComingFromBackend'")
  })

  it('keeps resource binding commands behind the query and mapper boundary', () => {
    for (const file of [
      'src/routes/resources/skills/+page.svelte',
      'src/routes/resources/mcp/+page.svelte',
      'src/lib/components/resources/ResourceBindingPanel.svelte'
    ]) {
      const source = readFileSync(file, 'utf-8')

      expect(source, file).not.toContain('ResourceBindingCommand')
      expect(source, file).toContain('ResourceBindingFormViewModel')
    }
  })

  it('keeps tools as the MCP-backed exposed tool catalog instead of a separate editable registry surface', () => {
    const tools = readFileSync('src/routes/resources/tools/+page.svelte', 'utf-8')
    const queries = readFileSync('src/lib/queries/resources.ts', 'utf-8')
    const api = readFileSync('src/lib/api/resources.ts', 'utf-8')

    expect(tools).not.toContain('ToolEditor')
    expect(tools).not.toContain('ResourceBindingPanel')
    expect(tools).not.toContain('useToolCommandMutations')
    expect(tools).not.toContain('useResourceBindMutation')
    expect(tools).not.toContain('useResourceUnbindMutation')
    expect(tools).not.toContain("t('resources.systemTools.readOnly')")
    expect(tools).toContain('useResourceToolsInfiniteQuery')
    expect(tools).toContain('combineToolPages')
    expect(tools).not.toContain('openCreateToolDialog')
    expect(tools).not.toContain('openEditToolDialog')
    expect(tools).not.toContain('disableTool')
    expect(tools).not.toContain('deleteTool')
    expect(tools).not.toContain("t('resources.action.delete')")
    expect(queries).toContain('registerTool')
    expect(queries).toContain('updateTool')
    expect(queries).toContain('deleteTool')
    expect(queries).toContain('useToolCommandMutations')
    expect(api).toContain('registerTool:')
    expect(api).toContain('updateTool:')
    expect(api).toContain('deleteTool:')
  })

  it('watches async resource infinite-scroll markers after list data renders', () => {
    for (const file of [
      'src/routes/resources/tools/+page.svelte',
      'src/routes/resources/skills/+page.svelte',
      'src/routes/resources/mcp/+page.svelte',
      'src/routes/resources/memory/+page.svelte'
    ]) {
      const source = readFileSync(file, 'utf-8')

      expect(source, file).toContain('IntersectionObserver')
      expect(source, file).toContain('bind:this={loadMoreMarker}')
      expect(source, file).toContain('$effect(() => {')
      expect(source, file).not.toContain('onMount(() =>')
    }
  })

  it('does not keep unmounted resource list components with partial command surfaces', () => {
    for (const file of [
      'src/lib/components/resources/ToolList.svelte',
      'src/lib/components/resources/SkillList.svelte',
      'src/lib/components/resources/McpServerList.svelte'
    ]) {
      expect(existsSync(file), file).toBe(false)
    }
  })

  it('keeps resource binding UI out of direct tool lifecycle editing', () => {
    const skills = readFileSync('src/routes/resources/skills/+page.svelte', 'utf-8')
    const mcp = readFileSync('src/routes/resources/mcp/+page.svelte', 'utf-8')
    const tools = readFileSync('src/routes/resources/tools/+page.svelte', 'utf-8')
    const toolEditor = readFileSync('src/lib/components/resources/ToolEditor.svelte', 'utf-8')
    const mcpEditor = readFileSync('src/lib/components/resources/McpServerEditor.svelte', 'utf-8')

    for (const [file, source] of [
      ['skills', skills],
      ['mcp', mcp]
    ] as const) {
      expect(source, file).toContain("import ResourceBindingPanel from '#lib/components/resources/ResourceBindingPanel.svelte'")
      expect(source, file).toContain('useResourceBindMutation')
      expect(source, file).toContain('useResourceUnbindMutation')
      expect(source, file).toContain('<ResourceBindingPanel')
    }
    expect(skills).toContain('resourceType="skill"')
    expect(mcp).toContain('resourceType="mcp_server"')
    expect(mcp).toContain('includeBindings: true')
    expect(mcp).toContain('relatedResources={mcpData.servers}')
    expect(mcp).toContain("'resources.message.mcpAuthRequired'")
    expect(mcp).toContain("'resources.message.mcpUnavailableSaved'")
    expect(mcp).toContain("availability_status === 'unavailable'")
    expect(mcp).toContain('function mcpResultServers')
    expect(mcp).toContain('Array.isArray(fields.servers)')
    expect(mcp).toContain('<Dialog open={showMcpEditorDialog}')
    expect(mcp).toContain('{#if actionMessage}')
    expect(mcp).toContain("import McpServerDeleteConfirmDialog from '#lib/components/resources/McpServerDeleteConfirmDialog.svelte'")
    expect(mcp).toContain('<McpServerDeleteConfirmDialog')
    expect(mcp).toContain('boundAgentCount > 0 || server.bindings.length > 0')
    expect(mcpEditor).toContain("t('resources.action.registerNew')")
    expect(mcpEditor).toContain('function startNewServer')
    expect(tools).not.toContain('<ResourceBindingPanel')
    expect(toolEditor).not.toContain("t('resources.systemTools.readOnly')")
    expect(toolEditor).toContain('onsubmit')
    expect(toolEditor).toContain('ondelete')
    expect(mcp).toContain('onnew={() => (selectedServer = undefined)}')
    expect(mcp).toContain("status === 'not_connected'")
    expect(mcp).toContain("'resources.mcp.notConnected'")
  })

  it('keeps skill management package import direct and binding tools behind dialogs', () => {
    const source = readFileSync('src/routes/resources/skills/+page.svelte', 'utf-8')

    expect(source).toContain("import Dialog from '#lib/components/ui/Dialog.svelte'")
    expect(source).toContain('showSkillBindingDialog')
    expect(source).toContain('importInput?.click()')
    expect(source).toContain("goto('/resources/skills/new?mode=edit')")
    expect(source).toContain("t('resources.skills.importPackage')")
    expect(source).toContain("t('resources.skills.create')")
    expect(source).toContain("import SkillCard from '#lib/components/resources/SkillCard.svelte'")
    expect(source).toContain('openSkillDetail')
    expect(source).toContain('openSkillEdit')
    expect(source).toContain('<Dialog open={showSkillBindingDialog}')
    expect(source).not.toContain('SkillEditor')
    expect(source).not.toContain('showSkillImportDialog')
    // Skills are rendered inline as cards; the binding dialog appears after the card grid.
    expect(source.indexOf('<Dialog open={showSkillBindingDialog}')).toBeGreaterThan(-1)
  })

  it('keeps skill card navigation backed by a real package detail route', () => {
    const source = readFileSync('src/routes/resources/skills/+page.svelte', 'utf-8')
    const detailSource = readFileSync('src/routes/resources/skills/[skillId]/+page.svelte', 'utf-8')

    expect(source).toContain('openSkillDetail')
    expect(source).toContain('openSkillEdit')
    expect(detailSource).toContain('useSkillDetailQuery')
    expect(detailSource).toContain('useSkillCommandMutations')
    expect(detailSource).toContain('mode=edit')
    expect(detailSource).toContain('SkillMarkdownPanel')
    expect(detailSource).toContain('saveMarkdown')
  })

  it('keeps skill detail resources behind the package file browser', () => {
    const detailSource = readFileSync('src/routes/resources/skills/[skillId]/+page.svelte', 'utf-8')

    expect(detailSource).toContain('SkillResourceBrowser')
    expect(detailSource).toContain('previewResourceFile')
    expect(detailSource).toContain('downloadResourceFile')
    expect(detailSource).toContain('saveResourceFile')
    expect(detailSource).toContain('uploadResourceFile')
    expect(detailSource).toContain('exportSkillPackage')
    expect(detailSource).not.toContain('root.children.slice(0, 5)')
  })

  it('keeps MCP management editor and binding tools behind dialogs', () => {
    const source = readFileSync('src/routes/resources/mcp/+page.svelte', 'utf-8')

    expect(source).toContain("import Dialog from '#lib/components/ui/Dialog.svelte'")
    expect(source).toContain('showMcpEditorDialog')
    expect(source).toContain('showMcpBindingDialog')
    expect(source).toContain("t('resources.mcp.registerOrDiscover')")
    expect(source).toContain("t('resources.mcp.editSelected')")
    expect(source).toContain("t('resources.mcp.bindSelected')")
    expect(source).toContain('<Dialog open={showMcpEditorDialog}')
    expect(source).toContain('<Dialog open={showMcpBindingDialog}')
    // MCP servers rendered inline as cards; dialogs appear after card grid
    expect(source.indexOf('<Dialog open={showMcpEditorDialog}')).toBeGreaterThan(-1)
    expect(source.indexOf('<Dialog open={showMcpBindingDialog}')).toBeGreaterThan(-1)
  })

  it('keeps profile and digest APIs while presenting the memory center by owner', () => {
    const api = readFileSync('src/lib/api/resources.ts', 'utf-8')
    const queries = readFileSync('src/lib/queries/resources.ts', 'utf-8')
    const mapper = readFileSync('src/lib/mappers/resource.ts', 'utf-8')
    const navigation = readFileSync('src/lib/routes/navigation.ts', 'utf-8')

    expect(existsSync('src/routes/resources/memory/+page.svelte')).toBe(true)
    const page = readFileSync('src/routes/resources/memory/+page.svelte', 'utf-8')

    expect(api).toContain('/api/resources/memory-profiles')
    expect(api).toContain('getPersonalMemoryProfileByAgent')
    expect(api).toContain('buildMemoryDigest')
    expect(queries).toContain('useMemoryProfilesInfiniteQuery')
    expect(queries).toContain('usePersonalMemoryProfileQuery')
    expect(queries).toContain('useMemoryDigestMutation')
    expect(mapper).toContain('mapMemoryProfileListView')
    expect(page).toContain('useMemoryProfilesInfiniteQuery')
    expect(page).not.toContain('usePersonalMemoryProfileQuery')
    expect(page).not.toContain('useMemoryDigestMutation')
    expect(page).toContain("type MemoryOwner = 'organization' | 'agent'")
    expect(page).toContain('data-memory-settings')
    expect(page).toContain('data-memory-candidates')
    expect(page).not.toContain('placeholder=')
    expect(navigation).toContain('/resources/memory')
  })

  it('keeps approval decision commands behind the query and mapper boundary', () => {
    const source = readFileSync('src/routes/approvals/+page.svelte', 'utf-8')

    expect(source).not.toContain('#lib/types/api')
    expect(source).not.toContain('action_id')
    expect(source).not.toContain('task_id')
    expect(source).not.toMatch(/body:\s*\{/)
    const detail = readFileSync('src/lib/components/workhub/WorkApprovalDetail.svelte', 'utf-8')
    expect(detail).toContain('ApprovalDecisionFormViewModel')
    expect(detail).toContain('useApprovalDecisionMutation')
    expect(detail).toContain('useHILDecisionMutation')
    expect(detail).toContain('actionPath: action.path')
    expect(source).toContain('legacyApprovalHref')
  })

  it('keeps mission discussion routes out of legacy mission discussion commands', () => {
    const source = readFileSync('src/routes/discussion/[missionId]/+page.svelte', 'utf-8')

    expect(source).not.toContain('#lib/types/api')
    expect(source).not.toContain('discussion_group_id')
    expect(source).not.toContain('message_kind')
    expect(source).not.toContain('visibility')
    expect(source).not.toMatch(/body:\s*\{/)
    expect(source).not.toContain('DiscussionMessageFormViewModel')
    expect(source).not.toContain('DiscussionReplyFormViewModel')
    expect(source).not.toContain('DiscussionWorkspace')
    expect(source).toContain('useMissionControlQuery')
  })

  it('redirects mission discussion aliases into the unified discussion workspace', () => {
    const page = readFileSync('src/routes/discussion/[missionId]/+page.svelte', 'utf-8')
    const queries = readFileSync('src/lib/queries/missions.ts', 'utf-8')

    expect(page).toContain("import { goto } from '$app/navigation'")
    expect(page).toContain('discussionConversationId')
    expect(page).toContain('goto(`/discussions/${encodeURIComponent(discussionConversationId)}`')
    expect(page).toContain('replaceState: true')
    expect(page).not.toContain("import MissionDiscussionPanel from '#lib/components/mission/MissionDiscussionPanel.svelte'")
    expect(page).not.toContain('useMissionDiscussionQuery')
    expect(page).not.toContain('useDiscussionMessageMutation')
    expect(page).not.toContain('useDiscussionReplyMutation')
    expect(page).not.toContain('subscribeMissionStream')
    expect(page).not.toContain('missionQueryKeys.discussion')
    expect(page).toContain('useMissionControlQuery(() => resolvedMissionId)')
    expect(queries).toContain("source.addEventListener('patch', schedule)")
    expect(queries).toContain("source.addEventListener('snapshot', schedule)")
    expect(queries).toContain('await invalidate()')
  })

  it('keeps discussion page behind the query and mapper boundary', () => {
    const source = readFileSync('src/lib/components/discussions/DiscussionWorkspace.svelte', 'utf-8')
    const discussionRoute = readFileSync('src/routes/discussions/+page.svelte', 'utf-8')

    expect(source).not.toContain('#lib/types/api')
    expect(source).not.toContain('conversation_id')
    expect(source).not.toMatch(/body:\s*\{/)
    expect(source).toContain('DiscussionMessageFormViewModel')
    expect(source).toContain('sendDiscussionMessageStream')
    expect(source).toContain('subscribeDiscussionStream')
    expect(source).toContain('selectDiscussion')
    expect(source).toContain('streamingDiscussion = undefined')
    expect(discussionRoute).toContain("import DiscussionWorkspace from '#lib/components/discussions/DiscussionWorkspace.svelte'")
  })

  it('keeps the discussion stream subscription stable while the POST send stream is rendering', () => {
    const source = readFileSync('src/lib/components/discussions/DiscussionWorkspace.svelte', 'utf-8')

    expect(source).toContain('if (!discussionId) return')
    expect(source).toContain('if (streamBusy && realtimeEventIsCoveredByActiveHttpStream(event)) return')
    expect(source).not.toContain('if (!discussionId || streamBusy) return')
  })

  it('uses the root route as the real Todo home page instead of a migrated discussion alias', () => {
    const pageSource = readFileSync('src/routes/+page.svelte', 'utf-8')
    const workItemsSource = readFileSync('src/routes/work-items/+page.svelte', 'utf-8')

    expect(existsSync('src/routes/+page.ts')).toBe(false)
    expect(pageSource).toContain('useTodoListQuery')
    expect(pageSource).toContain('useTodoCommandMutations')
    expect(pageSource).toContain('useWorkHubSummaryQuery')
    expect(pageSource).toContain('WorkTaskRow')
    expect(pageSource).toContain('IdeaReviewRow')
    expect(pageSource).not.toContain("import DiscussionWorkspace from '#lib/components/discussions/DiscussionWorkspace.svelte'")
    expect(pageSource).not.toContain('<DiscussionWorkspace />')
    expect(pageSource).not.toContain('useMissionInfiniteListQuery')
    expect(pageSource).not.toContain('<MissionList')
    expect(workItemsSource).toContain('useMissionInfiniteListQuery')
    expect(workItemsSource).toContain('buildWorkCenterItems')
  })

  it('keeps discussion running and error states visible in the native panel', () => {
    const source = readFileSync('src/lib/components/discussions/DiscussionPanel.svelte', 'utf-8')

    expect(source).toContain("t('discussionSession.running')")
    expect(source).toContain("t('discussionSession.sendFailed')")
    expect(source).toContain('discussion?.runtimeStatus ===')
    expect(source).toContain('busy || isRunning')
  })

  it('keeps mission create commands behind the query and mapper boundary', () => {
    const source = readFileSync('src/routes/missions/+page.svelte', 'utf-8')

    expect(source).not.toContain('#lib/types/api')
    expect(source).not.toContain('final_goal')
    expect(source).not.toContain('business_direction')
    expect(source).toContain('MissionCreateFormViewModel')
  })

  it('keeps squad page projections on view models instead of rehydrating API DTOs', () => {
    const source = readFileSync('src/routes/squads/+page.svelte', 'utf-8')

    expect(source).not.toContain('#lib/types/api')
    expect(source).not.toMatch(/\b(?:department|organization|member|agent|profile|real|display|employment|position|role|manager|authority|runtime)_[a-z_]+/)
    expect(source).toContain('mapSquadDirectoryFromOrgGraphView')
  })

  it('keeps mixed squad actions as real navigation instead of disabled placeholders', () => {
    const source = readFileSync('src/routes/squads/+page.svelte', 'utf-8')

    expect(source).not.toContain("import ProfileCard from '#lib/components/shared/ProfileCard.svelte'")
    expect(source).toContain('useWorkforceReleaseMutation')
    expect(source).toContain('useOrganizationUnitDeleteMutation')
    expect(source).not.toContain('<ProfileCard')
    expect(source).toContain('{#each squad.members as member}')
    expect(source).toContain('min-h-20 items-center gap-3')
    expect(source).toContain('href="/people"')
    expect(source).toContain("t('squads.deleteSquad')")
    expect(source).toContain("t('squads.deleteMember')")
    expect(source).not.toContain('squad.members.map((member) => member.profileId)')
    expect(source).not.toContain('squads.actionUnavailable')
    expect(source).not.toContain('<Button disabled><Plus')
    expect(source).not.toContain('disabled:cursor-not-allowed')
  })

  it('keeps visible form placeholders translated instead of leaking backend field names', () => {
    for (const file of sourceRoots
      .flatMap(collectFiles)
      .filter((entry) => entry.endsWith('.svelte'))) {
      const source = readFileSync(file, 'utf-8')

      expect(source, file).not.toMatch(/placeholder="(?:mission|actor|entity|agent|tool|resource|organization|department|required|business|final|real|display|profile|position)_/)
      expect(source, file).not.toMatch(/placeholder="(?:name|category|title|message|reason required)"/)
    }
  })

  it('uses visible input group labels instead of placeholders in business UI', () => {
    for (const file of sourceRoots
      .flatMap(collectFiles)
      .filter((entry) => entry.endsWith('.svelte') && !entry.endsWith('src/lib/components/ui/Input.svelte'))) {
      const source = readFileSync(file, 'utf-8')

      // Labels must remain visible; protocol examples do not need translation.
      const hardcodedPlaceholder = /placeholder="(?!https?:\/\/|secret:\/\/)[^{]/.test(source)
      expect(hardcodedPlaceholder, file).toBe(false)
    }
  })

  it('requires approval decision dialog chrome to use i18n', () => {
    const source = readFileSync('src/lib/components/approvals/ApprovalDecisionDialog.svelte', 'utf-8')

    expect(source).toContain("createTranslator($locale)")
    expect(source).toContain("t('approvals.")
    expect(source).not.toContain('reason required')
    expect(source).not.toContain('placeholder="message"')
  })

  it('keeps the people directory aligned with the React demo list workflow without local mock data', () => {
    const page = readFileSync('src/routes/people/+page.svelte', 'utf-8')
    const list = readFileSync('src/lib/components/operations/WorkforceMemberList.svelte', 'utf-8')
    const memberProfile = readFileSync('src/lib/components/operations/WorkforceMemberProfilePanel.svelte', 'utf-8')
    const profile = readFileSync('src/routes/profile/+page.svelte', 'utf-8')
    const topBar = readFileSync('src/lib/components/layout/TopBar.svelte', 'utf-8')

    expect(page).toContain("t('people.addPerson')")
    expect(page).toContain("t('people.registerAgent')")
    expect(page).not.toContain('EmploymentRecordsView')
    expect(memberProfile).toContain("t('people.employmentInformation')")
    expect(page).toContain("import WorkforceMemberProfilePanel from '#lib/components/operations/WorkforceMemberProfilePanel.svelte'")
    expect(page).toContain('onselect={selectMember}')
    expect(profile).toContain("import { profileApi } from '#lib/api/profile'")
    expect(profile).toContain('profileApi.startWeixinLogin')
    expect(profile).toContain('profileApi.setWeixinDefaultAgent')
    expect(topBar).toContain("goto('/profile')")
    expect(page).toContain('<Dialog open={selectedMember !== undefined}')
    expect(page).toContain('initial={editorInitial}')
    expect(page).toContain("defaultKind={editorKind}")
    expect(page).toContain('data-people-filter-bar="top"')
    expect(page).toContain("t('people.search')")
    expect(page).toContain("t('filter.department')")
    expect(page).toContain("t('filter.allDepartments')")
    expect(page).toContain("t('filter.allMembers')")
    expect(page).toContain('useResourceSkillsQuery')
    expect(page).toContain('useResourceToolsQuery')
    expect(page).toContain('useMcpServersQuery')
    expect(page).toContain('useOrgRolesQuery')
    expect(page).toContain('includeDetails: true')
    expect(page).toContain('skillOptions={skillOptions}')
    expect(page).toContain('toolOptions={toolOptions}')
    expect(page).toContain('roleOptions={roleOptions}')
    expect(page).not.toContain('skillOptionsFromMembers')
    expect(page).not.toContain('toolOptionsFromMembers')
    expect(page).not.toContain('useMemoryProfilesQuery')
    expect(page).not.toContain('personalMemoryOptions={memoryOptions}')
    expect(page).toContain('filteredMembers')
    expect(page).not.toContain('const allMembers')
    expect(list).toContain('data-workforce-list-layout="vertical"')
    expect(list).not.toContain('lg:grid-cols-2 xl:grid-cols-3')
    expect(list).toContain('onselect?: (member: WorkforceMemberViewModel) => void')
    expect(list).toContain('onselect?.(member)')
    expect(list).toContain('member.capabilityTags.slice(0, 4)')
    expect(list).toContain("t('workspace.agent')")
    expect(list).toContain("t('workspace.human')")
  })

  it('loads detailed talent market members while fetching the total independently', () => {
    const page = readFileSync('src/routes/people/+page.svelte', 'utf-8')
    const queries = readFileSync('src/lib/queries/workforce.ts', 'utf-8')

    expect(page).toContain('useTalentMarketCountQuery')
    expect(page).toContain('marketCountQuery')
    expect(page).toContain('marketCountQuery.data?.page?.total')
    expect(page).toMatch(/useTalentMarketMembersInfiniteQuery\(\(\) => \(\{\s*enabledOnly: undefined,\s*includeDetails: true\s*\}\), \(\) => placementView === 'market'/)
    expect(queries).toContain('marketCountFilters')
    expect(queries).toContain('limit: 1')
    expect(queries).toContain('offset: 0')
  })

  it('loads more people rows from a viewport sentinel instead of only an inner scroll handler', () => {
    const page = readFileSync('src/routes/people/+page.svelte', 'utf-8')

    expect(page).toContain('IntersectionObserver')
    expect(page).toContain('loadMoreSentinel')
    expect(page).toContain('bind:this={loadMoreSentinel}')
    expect(page).toContain("rootMargin: '320px 0px'")
    expect(page).toContain('loadMoreActiveMembers()')
  })

  it('keeps workforce detail and editor chrome localized with semantic controls', () => {
    const page = readFileSync('src/routes/people/+page.svelte', 'utf-8')
    const list = readFileSync('src/lib/components/operations/WorkforceMemberList.svelte', 'utf-8')
    const editor = readFileSync('src/lib/components/operations/WorkforceMemberEditor.svelte', 'utf-8')
    const profile = readFileSync('src/lib/components/operations/WorkforceMemberProfilePanel.svelte', 'utf-8')

    expect(page).toContain("t('people.editMember')")
    expect(page).toContain("t('people.profile')")
    expect(list).not.toContain('>View<')
    expect(editor).toContain("role=\"switch\"")
    expect(editor).toContain('<textarea')
    expect(editor).toContain("import Dialog from '#lib/components/ui/Dialog.svelte'")
    expect(editor).toContain('transferSummaryField(')
    expect(editor).toContain('transferField(')
    expect(editor).toContain("t('people.configureSkills')")
    expect(editor).toContain("t('people.configureTools')")
    expect(editor).toContain("'people.skillsAvailable'")
    expect(editor).toContain("'people.toolsAvailable'")
    expect(editor.indexOf("'people.skillsAvailable'")).toBeGreaterThan(editor.indexOf("<Dialog open={transferDialog === 'skills'}"))
    expect(editor.indexOf("'people.toolsAvailable'")).toBeGreaterThan(editor.indexOf("<Dialog open={transferDialog === 'tools'}"))
    expect(editor).toContain('multiple')
    expect(editor).toContain("'people.agentPrompt'")
    expect(editor).not.toContain("'Agent prompt'")
    expect(editor).not.toContain("'Skill refs'")
    expect(profile).toContain("createTranslator($locale)")
    expect(profile).toContain("t('people.notConfigured')")
    expect(profile).toContain("t('people.permissionsAndResponsibilities')")
    expect(profile).not.toContain('Not provided</p>')
    expect(profile).not.toContain('>Agent prompt<')
  })

  it('keeps the organization graph as a hierarchy without mock counts or flat department cards', () => {
    const source = readFileSync('src/routes/org/+page.svelte', 'utf-8')
    const nodeSource = readFileSync('src/lib/components/operations/OrgUnitNode.svelte', 'utf-8')

    expect(source).toContain("t('organization.totalHeadcount')")
    expect(source).toContain('organizationTree')
    expect(source).toContain("t('organization.addUnit')")
    expect(source).not.toContain('unitDefaultPositionName')
    expect(source).not.toContain('showCreatePositionDialog')
    expect(source).not.toContain('createPositionMutation')
    expect(nodeSource).toContain("t('organization.memberAffiliation'")
    expect(source).toContain('orgQuery.data?.members')
    expect(source).not.toContain('departmentCards')
    expect(source).not.toContain('{#each departmentCards')
    expect(source).not.toContain('<OrgGraphChart')
    expect(source).not.toContain('<span>{edge.from}</span>')
    expect(source).not.toContain('<span>{edge.to}</span>')
    expect(source).not.toMatch(/<div class="text-2xl font-semibold">5<\/div>/)
  })

  it('wires organization deletion through real backend mutations without mock data paths', () => {
    const source = readFileSync('src/routes/org/+page.svelte', 'utf-8')
    const api = readFileSync('src/lib/api/operations.ts', 'utf-8')
    const queries = readFileSync('src/lib/queries/workforce.ts', 'utf-8')

    expect(source).toContain('useOrganizationDeleteMutation')
    expect(source).toContain('useOrganizationUnitDeleteMutation')
    expect(source).toContain("'organization.delete'")
    expect(source).toContain("'organization.deleteUnit'")
    expect(source).toContain("'organization.confirmDelete'")
    expect(source).toContain("'organization.deleteSucceeded'")
    expect(source).toContain("'organization.unitDeleteSucceeded'")
    expect(source).not.toMatch(/mock|demo|sample/i)
    expect(api).toContain('deleteOrganization:')
    expect(api).toContain('/api/org/${organizationId}')
    expect(api).toContain('deleteOrganizationPosition:')
    expect(api).toContain('/api/org/positions/${positionId}')
    expect(queries).toContain('createOrganizationMutationRegistry')
    expect(queries).toContain('deleteOrganization')
    expect(queries).toContain('deleteOrganizationPosition')
  })

  it('lets organization detail remove scoped members before deleting organizations', () => {
    const source = readFileSync('src/routes/org/+page.svelte', 'utf-8')

    expect(source).toContain('useWorkforceReleaseMutation')
    expect(source).toContain('selectedMembers')
    expect(source).toContain('membersForSelectedOrganization')
    expect(source).toContain('releaseMemberFromSelection')
    expect(source).toContain("t('organization.removeMember')")
    expect(source).toContain("t('organization.confirmRemoveMember'")
    expect(source).not.toContain('(orgQuery.data?.members ?? []).slice(0, 6)')
  })

  it('keeps the organization structure focused on units and members', () => {
    const source = readFileSync('src/routes/org/+page.svelte', 'utf-8')
    const detail = readFileSync('src/lib/components/operations/OrganizationDetailPanel.svelte', 'utf-8')
    const api = readFileSync('src/lib/api/operations.ts', 'utf-8')

    expect(source).not.toContain('showCreatePositionDialog')
    expect(source).not.toContain('createPositionMutation')
    expect(source).not.toContain("t('organization.addPosition')")
    expect(source).not.toContain("t('organization.field.positionName')")
    expect(detail).toContain('OrganizationGraphWorkspace')
    expect(api).toContain('createOrganizationPosition:')
    expect(api).toContain('/api/org/positions')
  })

  it('creates organizations from a dialog with input group labels instead of placeholders', () => {
    const source = readFileSync('src/routes/org/+page.svelte', 'utf-8')

    expect(source).toContain("import Dialog from '#lib/components/ui/Dialog.svelte'")
    expect(source).toContain('showCreateOrgDialog')
    expect(source).toContain('useOrganizationCreateMutation')
    expect(source).toContain("t('organization.create')")
    expect(source).toContain("t('organization.field.name')")
    expect(source).not.toContain("t('organization.field.defaultDepartment')")
    expect(source).not.toContain("t('organization.field.defaultPosition')")
    expect(source).not.toContain("t('organization.field.organizationId')")
    expect(source).not.toContain('organizationId = $state')
    expect(source).not.toMatch(/placeholder=\{?t\('organization\.field\./)
  })

  it('does not ask users to type generated workforce ids when creating members', () => {
    const editor = readFileSync('src/lib/components/operations/WorkforceMemberEditor.svelte', 'utf-8')
    const mapper = readFileSync('src/lib/mappers/workforce.ts', 'utf-8')
    const queries = readFileSync('src/lib/queries/workforce.ts', 'utf-8')

    expect(editor).not.toContain('profileId = $state')
    expect(editor).not.toContain('agentId = $state')
    expect(editor).not.toContain("t('people.profileId')")
    expect(editor).not.toContain("t('people.agentId')")
    expect(mapper).not.toContain('agent_profile_id: form.profileId')
    expect(mapper).not.toContain('agent_id: form.agentId')
    expect(queries).toContain('operationsApi.createWorkforceMember')
  })

  it('creates workforce members from a dialog and never asks users to type org tree ids', () => {
    const page = readFileSync('src/routes/people/+page.svelte', 'utf-8')
    const editor = readFileSync('src/lib/components/operations/WorkforceMemberEditor.svelte', 'utf-8')

    expect(page).toContain("import Dialog from '#lib/components/ui/Dialog.svelte'")
    expect(page).toContain('showEditorDialog')
    expect(page).toContain('<Dialog open={showEditorDialog}')
    expect(page).toContain('useOrgGraphQuery(() => ({})')
    expect(page).toContain('organizationFilterOptions')
    expect(page).toContain('departmentFilterOptions')
    expect(page).not.toContain('<Input bind:value={organizationId}')
    expect(page).not.toContain("placeholder={t('filter.organizationId')}")
    expect(editor).toContain("selectField('organization.root', organizationId")
    expect(editor).toContain("selectField('people.department', departmentId")
    expect(editor).toContain("selectField('people.position', positionId")
    expect(editor).toContain("positionId = ''")
    expect(editor).not.toContain("placeholder={t('filter.organizationId')}")
    expect(editor).not.toContain("placeholder={t('filter.departmentId')}")
  })

  it('shows workforce affiliation with organization and unit names instead of raw ids or broken separators', () => {
    const detail = readFileSync('src/lib/components/operations/OrganizationDetailPanel.svelte', 'utf-8')
    const mapper = readFileSync('src/lib/mappers/workforceViews.ts', 'utf-8')
    const list = readFileSync('src/lib/components/operations/WorkforceMemberList.svelte', 'utf-8')

    expect(mapper).toContain('membersByOrganizationNode')
    expect(mapper).toContain('directMembers:')
    expect(detail).toContain('visibleMembers')
    expect(detail).toContain('member.departmentDisplayName')
    expect(detail).toContain('member.positionDisplayName')
    expect(detail).not.toContain('organizationNameById')
    expect(detail).not.toContain('`${member.organizationId} /')
    expect(list).not.toContain(' 路 ')
    expect(list).toContain('{roleLine(member)}')
    expect(list).toContain('member.roleLabels.slice(0, 3)')
  })

  it('keeps the people page from inlining the talent market hire dialog', () => {
    const page = readFileSync('src/routes/people/+page.svelte', 'utf-8')
    const dialog = readFileSync('src/lib/components/operations/TalentMarketHireDialog.svelte', 'utf-8')

    expect(page).toContain("import TalentMarketHireDialog from '#lib/components/operations/TalentMarketHireDialog.svelte'")
    expect(page).toContain('<TalentMarketHireDialog')
    expect(page).not.toContain("title={t('people.hireToUnit')}")
    expect(page).not.toContain('hireOrganizationId = $state')
    expect(dialog).toContain("t('people.hireToUnit')")
    expect(dialog).toContain("bind:value={organizationId}")
  })
})
