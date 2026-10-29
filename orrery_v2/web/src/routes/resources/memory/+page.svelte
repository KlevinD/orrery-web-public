<script lang="ts">
  import {
    AlertTriangle,
    Bot,
    Building2,
    CalendarDays,
    Check,
    ChevronRight,
    Eye,
    FileClock,
    History,
    Link2,
    ListChecks,
    Search,
    Settings2,
    X
  } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import ResourceRelationshipGraph from '#lib/components/graph/ResourceRelationshipGraph.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { TranslationKey } from '#lib/i18n'
  import { buildMemoryOntologyRelationshipGraph, memoryRecordOntologyTenantId } from '#lib/graph/resourceRelationships'
  import { countMeaningfulMemorySources, groupMemoryDiaryRecords, isMemoryDiaryRecord, memoryDiaryDate } from '#lib/memory/diary'
  import {
    useAcceptMemoryCandidateMutation,
    useMemoryCandidatesQuery,
    useMemoryRecordQuery,
    useMemoryRecordsByProfilesQuery,
    useRejectMemoryCandidateMutation
  } from '#lib/queries/memory'
  import {
    combineMemoryProfilePages,
    RESOURCE_CATALOG_PAGE_SIZE,
    useMemoryProfilesInfiniteQuery,
    useMemoryProfileUpdateMutation
  } from '#lib/queries/resources'
  import { useSettingsQuery } from '#lib/queries/settings'
  import { useOntologyContextNeighborhoodQuery } from '#lib/queries/ontology-context'
  import { useOrgGraphQuery, useWorkforceMembersQuery } from '#lib/queries/workforce'
  import { mapMemoryProfileListView } from '#lib/mappers/resource'
  import type { ApiClientError } from '#lib/api/client'
  import type { MemoryCandidateDTO, MemoryRecordDTO } from '#lib/types/memory'
  import type { MemoryProfileViewModel } from '#lib/types/resource-memory'
  import type { OrganizationTreeViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'

  type MemoryOwner = 'organization' | 'agent'
  type MemoryView = 'all' | 'run' | 'fact' | 'experience' | 'world'

  interface MemorySubject {
    id: string
    identity: string
    title: string
    subtitle: string
    avatarProfileId: string
    human: boolean
    profiles: MemoryProfileViewModel[]
    profileIds: string[]
    recordCount: number
    candidateCount: number
    enabled: boolean
  }

  let owner = $state<MemoryOwner>('organization')
  let search = $state('')
  let recordSearch = $state('')
  let includeHistory = $state(false)
  let resourceView = $state<'content' | 'graph'>('content')
  let selectedSubjectId = $state('')
  let memoryView = $state<MemoryView>('all')
  let settingsOpen = $state(false)
  let reviewOpen = $state(false)
  let selectedMemoryId = $state('')
  let graphMemoryId = $state('')
  let rejectReason = $state('')
  let candidateActionMessage = $state('')
  let settingsEnabled = $state(false)
  let settingsMaxRecords = $state('')
  let settingsMaxSummaryBytes = $state('')
  let settingsTags = $state('')
  let settingsMessage = $state('')
  let loadMoreMarker = $state<HTMLElement | undefined>()
  let t = $derived(createTranslator($locale))

  const memoryQuery = useMemoryProfilesInfiniteQuery(() => ({
    includeUsage: true,
    includeBindings: true,
    limit: RESOURCE_CATALOG_PAGE_SIZE
  }))
  const settingsQuery = useSettingsQuery()
  const organizationQuery = useOrgGraphQuery(() => ({}), () => owner === 'organization')
  const workforceQuery = useWorkforceMembersQuery(() => ({ includeDetails: false, limit: 1000 }), () => owner === 'agent')
  const memoryData = $derived(mapMemoryProfileListView(combineMemoryProfilePages(memoryQuery.data?.pages ?? [])))
  const organizationProfiles = $derived(memoryData.profiles.filter(isOrganizationProfile))
  const agentProfiles = $derived(memoryData.profiles.filter((profile) => profile.memoryKind === 'personal'))
  const organizationSubjects = $derived(mergeOrganizationDirectory(
    groupProfiles(organizationProfiles, 'organization'),
    organizationQuery.data?.organizations ?? []
  ))
  const agentSubjects = $derived(mergeAgentDirectory(
    groupProfiles(agentProfiles, 'agent'),
    workforceQuery.data?.members ?? []
  ))
  const directoryLoading = $derived(owner === 'agent' ? workforceQuery.isLoading : organizationQuery.isLoading)
  const directoryError = $derived(owner === 'agent' ? workforceQuery.error : organizationQuery.error)
  const allCurrentSubjects = $derived(owner === 'agent' ? agentSubjects : organizationSubjects)
  const currentSubjects = $derived(allCurrentSubjects.filter(matchesSubjectSearch))
  const selectedSubject = $derived(currentSubjects.find((subject) => subject.id === selectedSubjectId))
  const selectedProfile = $derived(
    selectedSubject?.profiles.find((profile) => profile.memoryKind === (owner === 'agent' ? 'personal' : 'organization'))
      ?? selectedSubject?.profiles[0]
  )

  const recordsQuery = useMemoryRecordsByProfilesQuery(
    () => selectedSubject?.profileIds ?? [],
    () => ({
      lifecycleStatus: includeHistory ? undefined : 'active',
      includeArchived: includeHistory,
      limit: 100
    }),
    () => Boolean(selectedSubjectId)
  )
  const candidatesQuery = useMemoryCandidatesQuery(
    () => ({
      scopeKind: selectedProfile?.defaultScopeKind,
      scopeId: selectedProfile?.defaultScopeId,
      status: 'proposed',
      limit: 100
    }),
    () => Boolean(reviewOpen && selectedProfile)
  )
  const recordQuery = useMemoryRecordQuery(() => selectedMemoryId)
  const acceptCandidateMutation = useAcceptMemoryCandidateMutation()
  const rejectCandidateMutation = useRejectMemoryCandidateMutation()
  const updateProfileMutation = useMemoryProfileUpdateMutation()

  const profileRecords = $derived(
    [...(recordsQuery.data?.records ?? [])].sort((left, right) => Date.parse(right.updated_at) - Date.parse(left.updated_at))
  )
  const visibleRecords = $derived(profileRecords.filter((record) => matchesMemoryView(record, memoryView) && matchesRecordSearch(record)))
  const diaryDays = $derived(groupMemoryDiaryRecords(visibleRecords))
  const pendingCandidates = $derived(candidatesQuery.data?.candidates ?? [])
  const graphMemory = $derived(profileRecords.find((record) => record.memory_id === graphMemoryId))
  const graphTenantId = $derived(graphMemory ? memoryRecordOntologyTenantId(graphMemory) : '')
  const ontologyGraphQuery = useOntologyContextNeighborhoodQuery(
    () => ({ tenantId: graphTenantId, rootId: graphMemoryId, depth: 1, maxNodes: 100, maxEdges: 200 }),
    () => resourceView === 'graph' && Boolean(graphMemoryId && graphTenantId)
  )
  const relationshipGraph = $derived(buildMemoryOntologyRelationshipGraph(ontologyGraphQuery.data, {
    memory: t('resources.memory.graphRecord'),
    entity: t('resources.memory.graphEntity'),
    degree: t('resources.memory.graphDegree'),
    confidence: t('resources.memory.confidence')
  }))
  const graphMemoryOptions = $derived(profileRecords.map((record) => ({ value: record.memory_id, label: recordTitle(record) })))
  const resourceViewTabs = $derived([
    { value: 'content', label: t('resources.graph.contentView') },
    { value: 'graph', label: t('resources.graph.graphView') }
  ])
  const ownerTabs = $derived([
    { value: 'organization', label: `${t('resources.memory.organizationMemory')}${organizationQuery.data ? ` ${organizationSubjects.length}` : ''}` },
    { value: 'agent', label: `${t('resources.memory.agentMemory')}${workforceQuery.data ? ` ${agentSubjects.length}` : ''}` }
  ])
  const memoryViewTabs = $derived(
    owner === 'agent'
      ? [
          memoryTab('all', 'resources.memory.allMemory'),
          memoryTab('run', 'resources.memory.runRecords'),
          memoryTab('fact', 'memory.value.layer.fact'),
          memoryTab('experience', 'memory.value.layer.experience')
        ]
      : [
          memoryTab('all', 'resources.memory.allMemory'),
          memoryTab('fact', 'resources.memory.organizationFacts'),
          memoryTab('experience', 'resources.memory.sharedExperience'),
          memoryTab('world', 'resources.memory.organizationRules')
        ]
  )
  const profileOptions = $derived(currentSubjects.map((subject) => ({
    value: subject.id,
    label: subject.title
  })))

  $effect(() => {
    owner
    selectedSubjectId = ''
    memoryView = 'all'
    settingsOpen = false
    reviewOpen = false
    recordSearch = ''
    includeHistory = false
  })

  $effect(() => {
    const subjects = currentSubjects
    if (!subjects.some((subject) => subject.id === selectedSubjectId)) {
      selectedSubjectId = subjects.find((subject) => subject.recordCount > 0)?.id ?? subjects[0]?.id ?? ''
    }
  })

  $effect(() => {
    selectedSubjectId
    selectedMemoryId = ''
    graphMemoryId = ''
    memoryView = 'all'
    recordSearch = ''
    includeHistory = false
  })

  $effect(() => {
    const records = profileRecords
    if (!records.some((record) => record.memory_id === graphMemoryId)) {
      graphMemoryId = records[0]?.memory_id ?? ''
    }
  })

  $effect(() => {
    if (!loadMoreMarker || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) loadMore()
    }, { rootMargin: '240px' })
    observer.observe(loadMoreMarker)
    return () => observer.disconnect()
  })

  function isOrganizationProfile(profile: MemoryProfileViewModel) {
    if (profile.memoryKind === 'personal') return false
    return profile.memoryKind === 'organization'
      || profile.memoryKind === 'collective'
      || profile.defaultScopeKind === 'organization'
      || profile.defaultScopeKind === 'department'
  }

  function groupProfiles(profiles: MemoryProfileViewModel[], subjectOwner: MemoryOwner) {
    const grouped = new Map<string, MemorySubject>()
    for (const profile of profiles) {
      const identity = profileIdentity(profile, subjectOwner)
      const id = `${subjectOwner}:${identity}`
      const existing = grouped.get(id)
      if (existing) {
        existing.profiles.push(profile)
        existing.profileIds.push(profile.id)
        existing.recordCount += profile.recordCount
        existing.candidateCount += profile.candidateCount
        existing.enabled ||= profile.enabled
        continue
      }
      grouped.set(id, {
        id,
        identity,
        title: identity,
        subtitle: '',
        avatarProfileId: '',
        human: false,
        profiles: [profile],
        profileIds: [profile.id],
        recordCount: profile.recordCount,
        candidateCount: profile.candidateCount,
        enabled: profile.enabled
      })
    }
    return [...grouped.values()].sort((left, right) => left.title.localeCompare(right.title))
  }

  function mergeOrganizationDirectory(profileSubjects: MemorySubject[], organizations: OrganizationTreeViewModel[]) {
    const remaining = new Map(profileSubjects.map((subject) => [subject.identity, subject]))
    const subjects = uniqueOrganizations(flattenOrganizations(organizations)).map((organization) => {
      const identity = firstValue(organization.organizationId, organization.id)
      const matched = remaining.get(identity)
      remaining.delete(identity)
      return {
        ...(matched ?? emptySubject('organization', identity)),
        id: `organization:${identity}`,
        identity,
        title: firstValue(organization.name, identity),
        subtitle: organization.displayCode || organization.code || '',
        enabled: matched?.enabled ?? organization.status !== 'inactive'
      }
    })
    return [...subjects, ...remaining.values()].sort((left, right) => left.title.localeCompare(right.title))
  }

  function flattenOrganizations(organizations: OrganizationTreeViewModel[]): OrganizationTreeViewModel[] {
    return organizations.flatMap((organization) => [
      organization,
      ...flattenOrganizations(organization.children ?? [])
    ])
  }

  function uniqueOrganizations(organizations: OrganizationTreeViewModel[]) {
    const seen = new Set<string>()
    return organizations.filter((organization) => {
      const identity = firstValue(organization.organizationId, organization.id)
      if (seen.has(identity)) return false
      seen.add(identity)
      return true
    })
  }

  function mergeAgentDirectory(profileSubjects: MemorySubject[], members: WorkforceMemberViewModel[]) {
    const remaining = new Map(profileSubjects.map((subject) => [subject.identity, subject]))
    const byProfileID = new Map(profileSubjects.flatMap((subject) => subject.profileIds.map((profileID) => [profileID, subject] as const)))
    const seen = new Set<string>()
    const subjects = members.filter((member) => {
      const identity = firstValue(member.profileId, member.id, member.agentInstanceId)
      if (seen.has(identity)) return false
      seen.add(identity)
      return true
    }).map((member) => {
      const identity = firstValue(member.profileId, member.id, member.agentInstanceId)
      const memoryProfileID = member.personalMemoryRef?.memoryProfileId
      const matched = (memoryProfileID ? byProfileID.get(memoryProfileID) : undefined)
        ?? [member.profileId, member.id, member.agentInstanceId]
          .map((candidate) => remaining.get(candidate))
          .find(Boolean)
      if (matched) remaining.delete(matched.identity)
      return {
        ...(matched ?? emptySubject('agent', identity)),
        id: `agent:${identity}`,
        identity,
        title: firstValue(member.displayName, member.realName, identity),
        subtitle: member.primaryRoleLabel || member.departmentDisplayName || '',
        avatarProfileId: member.profileId,
        human: member.agentKind.toLowerCase() === 'human',
        enabled: matched?.enabled ?? member.enabled
      }
    })
    return [...subjects, ...remaining.values()].sort((left, right) => left.title.localeCompare(right.title))
  }

  function emptySubject(subjectOwner: MemoryOwner, identity: string): MemorySubject {
    return {
      id: `${subjectOwner}:${identity}`,
      identity,
      title: identity,
      subtitle: '',
      avatarProfileId: '',
      human: false,
      profiles: [],
      profileIds: [],
      recordCount: 0,
      candidateCount: 0,
      enabled: false
    }
  }

  function matchesSubjectSearch(subject: MemorySubject) {
    const query = search.trim().toLocaleLowerCase()
    if (!query) return true
    return [subject.title, subject.identity, subject.subtitle].some((value) => value.toLocaleLowerCase().includes(query))
  }

  function profileIdentity(profile: MemoryProfileViewModel, subjectOwner: MemoryOwner) {
    if (subjectOwner === 'agent') {
      return firstValue(profile.ownerAgentId, profile.defaultScopeId, profile.id)
    }
    return firstValue(profile.organizationId, profile.defaultScopeId, profile.departmentId, profile.id)
  }

  function firstValue(...values: Array<string | undefined>) {
    return values.find((value) => hasValue(value)) ?? t('workspace.notProvided')
  }

  function hasValue(value: string | undefined) {
    return Boolean(value && value !== 'Not provided')
  }

  function displayField(value: string | undefined) {
    return hasValue(value) ? value! : t('workspace.notProvided')
  }

  function memoryTab(value: MemoryView, label: TranslationKey) {
    return { value, label: `${t(label)} ${countRecords(value)}` }
  }

  function matchesMemoryView(record: MemoryRecordDTO, view: MemoryView) {
    if (view === 'all') return true
    if (view === 'run') return isMemoryDiaryRecord(record)
    if (view === 'fact') return record.layer === 'fact' && !isMemoryDiaryRecord(record)
    return record.layer === view
  }

  function countRecords(view: MemoryView) {
    return profileRecords.filter((record) => matchesMemoryView(record, view)).length
  }

  function matchesRecordSearch(record: MemoryRecordDTO) {
    const query = recordSearch.trim().toLocaleLowerCase()
    if (!query) return true
    return [
      record.topic,
      record.summary,
      record.content,
      record.narrative,
      ...(record.facts ?? []),
      ...(record.lessons ?? []),
      ...(record.preferences ?? []),
      ...(record.warnings ?? []),
      ...(record.decisions ?? []),
      ...(record.tags ?? []),
      ...(record.source_memory_refs ?? []),
      ...(record.source_evidence_refs ?? [])
    ].some((value) => value?.toLocaleLowerCase().includes(query))
  }

  function recordSourceCount(record: MemoryRecordDTO) {
    return countMeaningfulMemorySources(record)
  }

  function recordTitle(record: MemoryRecordDTO) {
    const topic = record.topic?.trim() ?? ''
    if (!isTechnicalMemoryTopic(topic)) return topic
    if (isMemoryDiaryRecord(record)) return t('resources.memory.workMemoryForDate', { date: memoryDiaryDate(record) })
    if (record.layer === 'experience') return t('resources.memory.reusableExperience')
    if (record.layer === 'world') return t('resources.memory.organizationConsensus')
    if (topic === 'agent_result') return t('resources.memory.taskOutcome')
    if (topic === 'personal_assistant_exchange') return t('resources.memory.conversationMemory')
    return summaryTitle(record.summary, 'resources.memory.confirmedFact')
  }

  function candidateTitle(candidate: MemoryCandidateDTO) {
    const topic = candidate.topic?.trim() ?? ''
    if (!isTechnicalMemoryTopic(topic)) return topic
    if (candidate.proposed_layer === 'experience') return t('resources.memory.reusableExperience')
    if (candidate.proposed_layer === 'world') return t('resources.memory.organizationConsensus')
    if (topic === 'agent_result') return t('resources.memory.taskOutcome')
    if (topic === 'personal_assistant_exchange') return t('resources.memory.conversationMemory')
    return summaryTitle(candidate.summary, 'resources.memory.confirmedFact')
  }

  function summaryTitle(summary: string | undefined, fallback: TranslationKey) {
    const firstLine = naturalizeMemoryText(summary).split('\n').find(Boolean) ?? ''
    if (!firstLine) return t(fallback)
    return firstLine.length > 48 ? `${firstLine.slice(0, 48)}…` : firstLine
  }

  function recordBody(record: MemoryRecordDTO) {
    return [record.narrative, record.content]
      .map(naturalizeMemoryText)
      .find((value) => value && value !== naturalizeMemoryText(record.summary)) ?? ''
  }

  function recordLayerLabel(record: MemoryRecordDTO) {
    if (isMemoryDiaryRecord(record)) return t('resources.memory.workMemories')
    return t(`memory.value.layer.${record.layer}` as TranslationKey)
  }

  function isTechnicalMemoryTopic(topic: string) {
    const normalized = topic.toLocaleLowerCase().replace(/[\s-]+/g, '_')
    return !topic
      || normalized === 'agent_result'
      || normalized === 'personal_assistant_exchange'
      || topic.startsWith('memory/')
      || /^[a-z0-9_.:/-]+$/i.test(topic)
  }

  function naturalizeMemoryText(value: string | undefined): string {
    let text = value?.trim() ?? ''
    if (!text) return ''
    text = text.replace(/```[\s\S]*?```/g, '').replace(/`([^`]+)`/g, '$1').trim()

    const repeatedFacts = text.match(/^Repeated\s+.+?\s+pattern across\s+(\d+)\s+accepted facts\.?$/i)
    if (repeatedFacts) return t('resources.memory.repeatedFactsSummary', { count: Number(repeatedFacts[1]) })

    const experienceLesson = text.match(/^Review\s+.+?\s+evidence before repeating work; first source:\s*([\s\S]+)$/i)
    if (experienceLesson) {
      return t('resources.memory.experienceLesson', { source: naturalizeMemoryText(experienceLesson[1]) })
    }

    const worldSummary = text.match(/^Organization world memory for\s+.+?\s+consolidated from\s+(\d+)\s+accepted memory records\.?$/i)
    if (worldSummary) return t('resources.memory.worldSummary', { count: Number(worldSummary[1]) })

    const worldNarrative = text.match(/^The current\s+.+?\s+belief is based on\s+(\d+)\s+accepted\s+(fact|experience)\s+memory records\.?$/i)
    if (worldNarrative) {
      return t('resources.memory.worldNarrative', {
        count: Number(worldNarrative[1]),
        layer: t(`memory.value.layer.${worldNarrative[2]}` as TranslationKey)
      })
    }

    if (/^Use accepted\s+.+?\s+source memory before updating organization guidance\.?$/i.test(text)) {
      return t('resources.memory.worldLesson')
    }
    if (/^Organization guidance must remain traceable to its accepted source memories\.?$/i.test(text)) {
      return t('resources.memory.traceableWarning')
    }
    if (/^World memory is based on a sparse source set and should be treated with lower confidence\.?$/i.test(text)) {
      return t('resources.memory.sparseWarning')
    }
    if (/^Conflicting accepted world memory exists; do not silently overwrite prior belief\.?$/i.test(text)) {
      return t('resources.memory.conflictWarning')
    }
    if (/^Agent run\s+.+?\s+completed\.?$/i.test(text)) return t('resources.memory.workCompleted')
    if (/^No accepted source memory was available for\s+.+?\.?$/i.test(text)) return t('resources.memory.noAcceptedSource')

    return text
      .split('\n')
      .map(naturalizeMemoryLine)
      .filter(Boolean)
      .join('\n')
      .trim()
  }

  function naturalizeMemoryLine(value: string) {
    const line = value.trim()
    if (!line || /^(Diary|Mission|Task|Artifact|Evidence):/i.test(line)) return ''
    const labels: Array<[RegExp, TranslationKey]> = [
      [/^Summary:\s*/i, 'resources.memory.narrativeSummary'],
      [/^Deliverable:\s*/i, 'resources.memory.narrativeDeliverable'],
      [/^Decision:\s*/i, 'resources.memory.narrativeDecision'],
      [/^Request:\s*/i, 'resources.memory.narrativeRequest'],
      [/^(?:Blocker|Blocked):\s*/i, 'resources.memory.narrativeBlocker'],
      [/^User:\s*/i, 'resources.memory.narrativeUser'],
      [/^Assistant:\s*/i, 'resources.memory.narrativeAssistant']
    ]
    for (const [pattern, key] of labels) {
      if (pattern.test(line)) return t(key, { text: line.replace(pattern, '').trim() })
    }
    return line
  }

  function naturalizeMemoryList(values: string[] | undefined) {
    return (values ?? []).map(naturalizeMemoryText).filter(Boolean)
  }

  function recordOwnerLabel() {
    if (!selectedSubject) return t('workspace.notProvided')
    return t(owner === 'agent' ? 'resources.memory.personalOwner' : 'resources.memory.organizationOwner', {
      name: selectedSubject.title
    })
  }

  function recordCreatorLabel(createdBy: string) {
    const member = workforceQuery.data?.members.find((candidate) =>
      [candidate.id, candidate.profileId, candidate.agentInstanceId].includes(createdBy)
    )
    return firstValue(member?.displayName, member?.realName, t('resources.memory.memorySystem'))
  }

  function recordVisibilityLabel(mode: string) {
    if (mode === 'owner_only' || mode === 'private') return t('resources.memory.onlyOwnerVisible')
    if (mode === 'organization') return t('resources.memory.organizationVisible')
    if (mode === 'public') return t('resources.memory.publicVisible')
    return t('resources.memory.relatedMembersVisible')
  }

  function confidenceLabel(confidence: number) {
    const normalized = confidence > 1 ? confidence / 100 : confidence
    if (normalized >= 0.8) return t('resources.memory.confidenceHigh')
    if (normalized >= 0.55) return t('resources.memory.confidenceMedium')
    return t('resources.memory.confidenceLow')
  }

  function sensitivityLabel(value: string) {
    const labels: Record<string, TranslationKey> = {
      personal: 'resources.memory.sensitivityPersonal',
      compensation: 'resources.memory.sensitivityCompensation',
      candidate: 'resources.memory.sensitivityRestricted',
      performance: 'resources.memory.sensitivityPerformance',
      legal: 'resources.memory.sensitivityLegal',
      executive: 'resources.memory.sensitivityExecutive',
      secret: 'resources.memory.sensitivitySecret'
    }
    return labels[value] ? t(labels[value]) : ''
  }

  function lifecycleLabel(status: string) {
    return t(`memory.value.lifecycle.${status}` as TranslationKey)
  }

  function visibilityLabel(profile: MemoryProfileViewModel) {
    const raw = profile.visibilityLabel
    if (!hasValue(raw)) return t('workspace.notProvided')
    const mode = raw.match(/^([^\s(]+)/)?.[1] ?? raw
    if (mode === 'organization') return t('resources.memory.organizationVisible')
    return t(`memory.value.visibility.${mode === 'owner' ? 'owner_only' : mode}` as TranslationKey)
  }

  function timeLabel(value: string | undefined) {
    if (!value) return t('workspace.notProvided')
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return date.toLocaleString($locale, { year: 'numeric', month: 'short', day: 'numeric' })
  }

  function openReview() {
    candidateActionMessage = ''
    rejectReason = ''
    reviewOpen = true
  }

  function openSettings() {
    if (!selectedProfile) return
    settingsEnabled = selectedProfile.enabled
    settingsMaxRecords = String(selectedProfile.maxRecords)
    settingsMaxSummaryBytes = String(selectedProfile.maxSummaryBytes)
    settingsTags = selectedProfile.retrievalTags.join(', ')
    settingsMessage = ''
    settingsOpen = true
  }

  function openMemoryDetail(memoryId: string) {
    selectedMemoryId = memoryId
  }

  function saveSettings() {
    if (!selectedProfile) return
    const maxRecords = Number.parseInt(settingsMaxRecords, 10)
    const maxSummaryBytes = Number.parseInt(settingsMaxSummaryBytes, 10)
    if (!Number.isInteger(maxRecords) || maxRecords <= 0 || !Number.isInteger(maxSummaryBytes) || maxSummaryBytes <= 0) {
      settingsMessage = t('resources.memory.settingsInvalid')
      return
    }
    settingsMessage = ''
    updateProfileMutation.mutate({
      profileId: selectedProfile.id,
      enabled: settingsEnabled,
      maxRecords,
      maxSummaryBytes,
      retrievalTags: settingsTags.split(',').map((tag) => tag.trim()).filter(Boolean)
    }, {
      onSuccess: () => (settingsMessage = t('resources.memory.settingsSaved')),
      onError: (error) => (settingsMessage = error instanceof Error ? error.message : t('resources.memory.settingsSaveFailed'))
    })
  }

  function acceptCandidate(candidateId: string) {
    candidateActionMessage = ''
    acceptCandidateMutation.mutate(
      { candidateId, profileId: selectedProfile?.id, reason: t('resources.memory.reviewAcceptedReason') },
      {
        onSuccess: () => (candidateActionMessage = t('resources.memory.candidateAccepted')),
        onError: (error) => (candidateActionMessage = error instanceof Error ? error.message : t('resources.memory.candidateActionFailed'))
      }
    )
  }

  function rejectCandidate(candidateId: string) {
    const reason = rejectReason.trim()
    if (!reason) return
    candidateActionMessage = ''
    rejectCandidateMutation.mutate(
      { candidateId, reason },
      {
        onSuccess: () => {
          candidateActionMessage = t('resources.memory.candidateRejected')
          rejectReason = ''
        },
        onError: (error) => (candidateActionMessage = error instanceof Error ? error.message : t('resources.memory.candidateActionFailed'))
      }
    )
  }

  function loadMore() {
    if (memoryQuery.hasNextPage && !memoryQuery.isFetchingNextPage) memoryQuery.fetchNextPage()
  }
</script>

<svelte:head>
  <title>{t('nav.memoryResources')} | Orrery</title>
</svelte:head>

<div class="page-shell max-w-[1120px] space-y-4">
  <header>
    <h1 class="page-title">{t('nav.memoryResources')}</h1>
    <p class="page-description">{t('nav.memoryResources.description')}</p>
  </header>

  {#if memoryQuery.isLoading || directoryLoading}
    <LoadingState title={t('state.loadingMemoryProfiles')} description={t('state.loadingMemoryProfilesDescription')} />
  {:else if memoryQuery.isError || directoryError}
    {@const error = (memoryQuery.error ?? directoryError) as ApiClientError}
    <ErrorState title={t('state.errorMemoryProfiles')} message={error.message} correlationId={error.correlationId} />
  {:else}
    {#if settingsQuery.data && !settingsQuery.data.memory.enabled}
      <div class="flex flex-col gap-3 rounded-lg border border-status-warning/35 bg-status-warning/5 px-4 py-3 sm:flex-row sm:items-center sm:justify-between" data-memory-disabled>
        <div class="flex min-w-0 items-start gap-3">
          <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0 text-status-warning" />
          <div>
            <p class="text-[13px] font-medium text-foreground">{t('resources.memory.captureDisabled')}</p>
            <p class="mt-0.5 text-xs leading-5 text-muted-foreground">{t('resources.memory.captureDisabledDescription')}</p>
          </div>
        </div>
        <Button variant="outline" size="sm" href="/settings">{t('resources.memory.openSettings')}</Button>
      </div>
    {/if}
    <section class="overflow-hidden rounded-lg border border-border bg-card" data-memory-center>
      <div class="flex flex-col gap-3 border-b border-border p-3 sm:flex-row sm:items-center sm:justify-between">
        <Tabs bind:value={owner} tabs={ownerTabs} selectionMaterial ariaLabel={t('resources.memory.ownerMode')} />
        <label class="relative w-full sm:max-w-xs">
          <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            oninput={(event) => (search = event.currentTarget.value)}
            ariaLabel={t('resources.memory.searchOwners')}
            class="pl-9"
          />
        </label>
      </div>

      <div class="grid min-h-[560px] lg:grid-cols-[260px_minmax(0,1fr)]">
        <aside class="border-b border-border bg-muted/10 lg:border-b-0 lg:border-r" aria-label={t('resources.memory.ownerList')}>
          <div class="flex items-center gap-2 border-b border-border px-4 py-3">
            {#if owner === 'agent'}
              <Bot class="h-4 w-4 text-brand" />
              <h2 class="text-[13px] font-semibold text-foreground">{t('resources.memory.agentMemory')}</h2>
            {:else}
              <Building2 class="h-4 w-4 text-brand" />
              <h2 class="text-[13px] font-semibold text-foreground">{t('resources.memory.organizationMemory')}</h2>
            {/if}
          </div>

          {#if currentSubjects.length === 0}
            <div class="px-4 py-10 text-center">
              <p class="text-sm font-medium text-foreground">{t('resources.memory.noOwners')}</p>
            </div>
          {:else}
            <div class="border-b border-border p-3 lg:hidden">
              <Select bind:value={selectedSubjectId} options={profileOptions} />
            </div>
            <div class="hidden lg:grid" data-memory-owner-list>
              {#each currentSubjects as subject (subject.id)}
                <button
                  type="button"
                  aria-pressed={selectedSubjectId === subject.id}
                  class="group flex min-w-0 items-center gap-3 border-b border-l-2 px-3 py-3 text-left transition-colors hover:bg-muted/45 {selectedSubjectId === subject.id ? 'selection-wash border-l-brand' : 'border-l-transparent'}"
                  onclick={() => (selectedSubjectId = subject.id)}
                >
                  {#if owner === 'agent'}
                    <WorkforceAvatar
                      profileId={subject.avatarProfileId}
                      name={subject.title}
                      human={subject.human}
                      class="h-9 w-9 rounded-md"
                      textClass="text-[11px]"
                    />
                  {:else}
                    <span class="grid h-9 w-9 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground {selectedSubjectId === subject.id ? 'text-brand' : ''}">
                      <Building2 class="h-4 w-4" />
                    </span>
                  {/if}
                  <span class="min-w-0 flex-1">
                    <span class="flex items-center gap-2">
                      <span class="truncate text-[13px] font-medium text-foreground">{subject.title}</span>
                      <span class="h-1.5 w-1.5 shrink-0 rounded-full {subject.enabled ? 'bg-status-success' : 'bg-muted-foreground/40'}"></span>
                    </span>
                    <span class="mt-0.5 block truncate text-[11px] text-muted-foreground">
                      {subject.subtitle ? `${subject.subtitle} · ` : ''}{subject.recordCount} {t('resources.memory.recordsUnit')}
                    </span>
                  </span>
                  <ChevronRight class="h-3.5 w-3.5 shrink-0 text-muted-foreground/45 group-hover:text-foreground" />
                </button>
              {/each}
            </div>
          {/if}
          <div bind:this={loadMoreMarker} class="h-px"></div>
        </aside>

        <main class="min-w-0" data-memory-owner-workspace>
          {#if selectedSubject}
            <header class="flex flex-col gap-3 border-b border-border px-4 py-4 sm:flex-row sm:items-center sm:justify-between sm:px-5">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <h2 class="truncate text-base font-semibold text-foreground">{selectedSubject.title}</h2>
                  <Badge tone={selectedSubject.enabled ? 'success' : 'neutral'}>
                    {selectedSubject.enabled ? t('kpi.enabled') : t('kpi.disabled')}
                  </Badge>
                </div>
                <p class="mt-1 text-xs text-muted-foreground">
                  {owner === 'agent' ? t('resources.memory.agentMemory') : t('resources.memory.organizationMemory')}
                  · {selectedSubject.recordCount} {t('resources.memory.recordsUnit')}
                </p>
              </div>
              {#if selectedProfile}
                <div class="flex shrink-0 items-center gap-2">
                  <Button variant="outline" size="sm" onclick={openReview}>
                    <ListChecks class="h-4 w-4" />
                    {t('resources.memory.pendingReview')}
                    {#if selectedSubject.candidateCount > 0}
                      <Badge tone="info">{selectedSubject.candidateCount}</Badge>
                    {/if}
                  </Button>
                  <Button
                    variant="outline"
                    size="icon"
                    ariaLabel={t('resources.memory.memorySettings')}
                    title={t('resources.memory.memorySettings')}
                    onclick={openSettings}
                  >
                    <Settings2 class="h-4 w-4" />
                  </Button>
                </div>
              {/if}
            </header>

            <div class="border-b border-border px-4 py-3 sm:px-5">
              <Tabs bind:value={resourceView} tabs={resourceViewTabs} selectionMaterial ariaLabel={t('resources.graph.viewMode')} />
            </div>

            {#if resourceView === 'graph'}
              <div class="border-b border-border px-4 py-3 sm:px-5">
                <label class="block max-w-md">
                  <span class="mb-1.5 block text-[11px] font-medium text-muted-foreground">{t('resources.memory.graphChooseRecord')}</span>
                  <Select bind:value={graphMemoryId} options={graphMemoryOptions} />
                </label>
              </div>
              {#if ontologyGraphQuery.isError}
                <div class="p-4 sm:p-5">
                  <ErrorState title={t('resources.memory.graphTitle')} message={ontologyGraphQuery.error.message} />
                </div>
              {:else}
                <ResourceRelationshipGraph
                  embedded
                  title={t('resources.memory.graphTitle')}
                  description={t('resources.memory.graphDescription')}
                  nodes={relationshipGraph.nodes}
                  links={relationshipGraph.links}
                  legend={[
                    { label: t('resources.memory.graphRecord'), color: '#353b45' },
                    { label: t('resources.memory.graphEntity'), color: '#4f83cc' },
                    { label: t('resources.memory.graphOntologyRelation'), color: '#7e8796' }
                  ]}
                  ariaLabel={t('resources.memory.graphTitle')}
                  emptyLabel={t('resources.memory.graphEmpty')}
                  openLabel={t('resources.graph.open')}
                  selectHint={t('resources.graph.selectHint')}
                  nodeCountLabel={t('resources.graph.nodes', { count: relationshipGraph.nodes.length })}
                  relationCountLabel={t('resources.graph.relations', { count: relationshipGraph.links.length })}
                  loading={recordsQuery.isLoading || ontologyGraphQuery.isLoading}
                  loadingLabel={t('resources.graph.loading')}
                  onopen={openMemoryDetail}
                />
              {/if}
            {:else}
              <Tabs
                bind:value={memoryView}
                tabs={memoryViewTabs}
                variant="underline"
                ariaLabel={t('resources.memory.contentViews')}
                class="overflow-x-auto px-4 sm:px-5"
              />

              <div class="border-b border-border px-4 py-3 sm:px-5" data-memory-content-toolbar>
                <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
                  <label class="relative min-w-0 flex-1">
                    <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                    <Input
                      value={recordSearch}
                      oninput={(event) => (recordSearch = event.currentTarget.value)}
                      ariaLabel={t('resources.memory.searchContent')}
                      class="pl-9"
                    />
                  </label>
                  <Button
                    variant={includeHistory ? 'secondary' : 'outline'}
                    size="sm"
                    onclick={() => (includeHistory = !includeHistory)}
                  >
                    <History class="h-4 w-4" />
                    {t('resources.memory.includeHistory')}
                  </Button>
                </div>
              </div>

              <div class="p-4 sm:p-5" data-memory-content>
              {#if selectedSubject.profileIds.length === 0}
                <div class="py-14 text-center">
                  <Settings2 class="mx-auto h-6 w-6 text-muted-foreground/45" />
                  <p class="mt-3 text-sm font-medium text-foreground">{t('resources.memory.noProfile')}</p>
                  <p class="mx-auto mt-1 max-w-md text-xs leading-5 text-muted-foreground">{t('resources.memory.noProfileDescription')}</p>
                </div>
              {:else if recordsQuery.isLoading}
                <div class="py-12 text-center text-xs text-muted-foreground">{t('resources.memory.loadingRecords')}</div>
              {:else if recordsQuery.isError}
                <div class="border-y border-destructive/20 bg-destructive/5 px-3 py-4 text-sm text-destructive">
                  {t('resources.memory.recordsLoadFailed')}
                </div>
              {:else if visibleRecords.length === 0}
                <div class="py-14 text-center">
                  <FileClock class="mx-auto h-6 w-6 text-muted-foreground/45" />
                  <p class="mt-3 text-sm font-medium text-foreground">{t('resources.memory.noRetainedContent')}</p>
                </div>
              {:else if memoryView === 'run'}
                <div class="space-y-5" data-memory-diary>
                  {#each diaryDays as day (day.date)}
                    <section>
                      <div class="flex items-center gap-2 border-b border-border pb-2">
                        <CalendarDays class="h-4 w-4 text-brand" />
                        <h3 class="text-[13px] font-semibold text-foreground">{day.date}</h3>
                        <span class="truncate text-[10.5px] text-muted-foreground">{t('resources.memory.memoryEntryCount', { count: day.records.length })}</span>
                      </div>
                      <div class="divide-y divide-border">
                        {#each day.records as record (record.memory_id)}
                          {@render memoryRecordRow(record)}
                        {/each}
                      </div>
                    </section>
                  {/each}
                </div>
              {:else}
                <div class="divide-y divide-border">
                  {#each visibleRecords as record (record.memory_id)}
                    {@render memoryRecordRow(record)}
                  {/each}
                </div>
              {/if}
              </div>
            {/if}
          {:else}
            <div class="grid min-h-[480px] place-items-center px-5 text-center">
              <div>
                {#if owner === 'agent'}
                  <Bot class="mx-auto h-7 w-7 text-muted-foreground/45" />
                {:else}
                  <Building2 class="mx-auto h-7 w-7 text-muted-foreground/45" />
                {/if}
                <p class="mt-3 text-sm font-medium text-foreground">{t('resources.memory.noOwners')}</p>
              </div>
            </div>
          {/if}
        </main>
      </div>
    </section>
  {/if}
</div>

<Dialog open={settingsOpen && Boolean(selectedProfile && selectedSubject)} title={t('resources.memory.memorySettings')} onClose={() => (settingsOpen = false)}>
  {#if selectedProfile && selectedSubject}
    <div data-memory-settings>
      <dl class="grid border-y border-border sm:grid-cols-2">
        {@render settingsField('resources.memory.scope', recordOwnerLabel())}
        {@render settingsField('resources.memory.visibility', visibilityLabel(selectedProfile))}
        {@render settingsField('resources.filter.bound', String(selectedProfile.boundAgents.length))}
      </dl>
      <div class="mt-5 grid gap-4 sm:grid-cols-2">
        <label class="block">
          <span class="mb-1.5 block text-[11px] font-medium text-muted-foreground">{t('resources.memory.maxRecords')}</span>
          <Input bind:value={settingsMaxRecords} type="number" inputmode="numeric" ariaLabel={t('resources.memory.maxRecords')} />
        </label>
        <label class="block">
          <span class="mb-1.5 block text-[11px] font-medium text-muted-foreground">{t('resources.memory.maxSummary')}</span>
          <Input bind:value={settingsMaxSummaryBytes} type="number" inputmode="numeric" ariaLabel={t('resources.memory.maxSummary')} />
        </label>
        <label class="block sm:col-span-2">
          <span class="mb-1.5 block text-[11px] font-medium text-muted-foreground">{t('resources.memory.tags')}</span>
          <Input bind:value={settingsTags} ariaLabel={t('resources.memory.tags')} />
        </label>
        <label class="flex items-center gap-2 text-[13px] text-foreground sm:col-span-2">
          <input class="h-4 w-4 rounded border-border accent-brand" type="checkbox" bind:checked={settingsEnabled} />
          {t('resources.memory.profileEnabled')}
        </label>
      </div>
      {#if settingsMessage}
        <p class="mt-4 text-xs text-muted-foreground" data-memory-settings-message>{settingsMessage}</p>
      {/if}
      <div class="mt-5 flex justify-end">
        <Button disabled={updateProfileMutation.isPending} onclick={saveSettings}>
          {updateProfileMutation.isPending ? t('resources.memory.saving') : t('resources.memory.saveSettings')}
        </Button>
      </div>
    </div>
  {/if}
</Dialog>

<Dialog open={reviewOpen && Boolean(selectedProfile)} title={t('resources.memory.pendingReview')} size="wide" onClose={() => (reviewOpen = false)}>
  <div data-memory-candidates>
    <label class="block max-w-sm">
      <span class="mb-1.5 block text-[11px] font-medium text-muted-foreground">{t('resources.memory.rejectReason')}</span>
      <Input bind:value={rejectReason} ariaLabel={t('resources.memory.rejectReason')} />
    </label>
    {#if candidateActionMessage}
      <p class="selection-wash mt-3 rounded-md px-3 py-2 text-xs text-foreground">{candidateActionMessage}</p>
    {/if}
    {#if candidatesQuery.isLoading}
      <div class="py-12 text-center text-xs text-muted-foreground">{t('memory.loadingCandidates')}</div>
    {:else if candidatesQuery.isError}
      <div class="mt-4 border-y border-destructive/20 bg-destructive/5 px-3 py-4 text-sm text-destructive">
        {t('resources.memory.candidatesLoadFailed')}
      </div>
    {:else if pendingCandidates.length === 0}
      <div class="py-14 text-center">
        <Check class="mx-auto h-6 w-6 text-status-success" />
        <p class="mt-3 text-sm font-medium text-foreground">{t('resources.memory.noPendingCandidates')}</p>
      </div>
    {:else}
      <div class="mt-4 divide-y divide-border border-t border-border">
        {#each pendingCandidates as candidate (candidate.candidate_id)}
          <article class="py-4">
            <div class="flex flex-col gap-3 sm:flex-row sm:items-start">
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <h3 class="text-[13.5px] font-semibold text-foreground">{candidateTitle(candidate)}</h3>
                  <Badge>{t(`memory.value.layer.${candidate.proposed_layer}` as TranslationKey)}</Badge>
                </div>
                <p class="mt-2 whitespace-pre-wrap text-[13px] leading-6 text-foreground/90">{naturalizeMemoryText(candidate.summary)}</p>
                <p class="mt-2 text-[10.5px] text-muted-foreground">
                  {recordOwnerLabel()} · {timeLabel(candidate.created_at)}
                </p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <Button size="sm" disabled={acceptCandidateMutation.isPending || rejectCandidateMutation.isPending} onclick={() => acceptCandidate(candidate.candidate_id)}>
                  <Check class="h-3.5 w-3.5" />
                  {t('resources.memory.accept')}
                </Button>
                <Button variant="outline" size="sm" disabled={!rejectReason.trim() || acceptCandidateMutation.isPending || rejectCandidateMutation.isPending} onclick={() => rejectCandidate(candidate.candidate_id)}>
                  <X class="h-3.5 w-3.5" />
                  {t('resources.memory.reject')}
                </Button>
              </div>
            </div>
          </article>
        {/each}
      </div>
    {/if}
  </div>
</Dialog>

<Dialog open={Boolean(selectedMemoryId)} title={t('resources.memory.memoryDetail')} size="wide" onClose={() => (selectedMemoryId = '')}>
  {#if selectedMemoryId && recordQuery.isLoading}
    <LoadingState title={t('resources.memory.memoryDetail')} description={t('resources.memory.loadingRecords')} />
  {:else if recordQuery.isError}
    <ErrorState title={t('resources.memory.memoryDetail')} message={t('resources.memory.recordsLoadFailed')} />
  {:else if recordQuery.data}
    {@const record = recordQuery.data}
    {@const sourceCount = recordSourceCount(record)}
    {@const sensitivity = sensitivityLabel(record.sensitivity)}
    <div class="space-y-5" data-memory-record-detail>
      <div>
        <div class="flex flex-wrap gap-2">
          <Badge tone="success">{lifecycleLabel(record.lifecycle_status)}</Badge>
          <Badge tone={isMemoryDiaryRecord(record) ? 'teal' : 'neutral'}>{recordLayerLabel(record)}</Badge>
          {#if sensitivity}<Badge>{sensitivity}</Badge>{/if}
        </div>
        <h3 class="mt-3 text-base font-semibold text-foreground">{recordTitle(record)}</h3>
        <p class="mt-2 whitespace-pre-wrap text-sm leading-6 text-foreground">{naturalizeMemoryText(record.summary)}</p>
      </div>
      <dl class="grid gap-x-6 border-y border-border sm:grid-cols-2">
        {@render detailRow('resources.memory.memoryOwner', recordOwnerLabel())}
        {@render detailRow('memory.visibility', recordVisibilityLabel(record.visibility.mode))}
        {@render detailRow('memory.createdBy', recordCreatorLabel(record.created_by))}
        {@render detailRow('memory.createdAt', timeLabel(record.created_at))}
        {@render detailRow('resources.memory.updatedAt', timeLabel(record.updated_at))}
        {@render detailRow('resources.memory.confidence', confidenceLabel(record.confidence))}
      </dl>
      {@render textBlock('memory.content', recordBody(record))}
      {@render detailList('memory.facts', naturalizeMemoryList(record.facts))}
      {@render detailList('memory.lessons', naturalizeMemoryList(record.lessons))}
      {@render detailList('resources.memory.preferences', naturalizeMemoryList(record.preferences))}
      {@render detailList('memory.warnings', naturalizeMemoryList(record.warnings))}
      {@render detailList('resources.memory.decisions', naturalizeMemoryList(record.decisions))}
      {#if sourceCount > 0}
        {@render textBlock('memory.sources', t('resources.memory.sourceExplanation', { count: sourceCount }))}
      {/if}
    </div>
  {/if}
</Dialog>

{#snippet memoryRecordRow(record: MemoryRecordDTO)}
  {@const body = recordBody(record)}
  {@const sourceCount = recordSourceCount(record)}
  <article class="py-4 first:pt-0 last:pb-0">
    <div class="flex items-start gap-3">
      <button
        type="button"
        class="group -m-2 min-w-0 flex-1 cursor-pointer rounded-md p-2 text-left transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
        aria-label={t('resources.memory.viewDetail') + ': ' + recordTitle(record)}
        data-memory-record-trigger
        onclick={() => openMemoryDetail(record.memory_id)}
      >
        <span class="flex flex-wrap items-center gap-2">
          <span class="min-w-0 text-[13.5px] font-semibold text-foreground group-hover:text-brand">{recordTitle(record)}</span>
          <Badge tone={isMemoryDiaryRecord(record) ? 'teal' : 'neutral'}>{recordLayerLabel(record)}</Badge>
          <Badge tone={record.lifecycle_status === 'active' ? 'success' : 'neutral'}>{lifecycleLabel(record.lifecycle_status)}</Badge>
          {#if (record.decisions?.length ?? 0) > 0}
            <Badge tone="info">{t('resources.memory.decisionCount', { count: record.decisions?.length ?? 0 })}</Badge>
          {/if}
        </span>
        <span class="mt-2 block whitespace-pre-wrap text-[13px] leading-6 text-foreground/90">{naturalizeMemoryText(record.summary)}</span>
        {#if body}
          <span class="mt-2 block line-clamp-3 whitespace-pre-wrap text-xs leading-5 text-muted-foreground">{body}</span>
        {/if}
        <span class="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-[10.5px] text-muted-foreground">
          <span>{t('resources.memory.updatedAtValue', { time: timeLabel(record.updated_at) })}</span>
          <span>{confidenceLabel(record.confidence)}</span>
          <span>{recordOwnerLabel()}</span>
          {#if sourceCount > 0}
            <span class="inline-flex items-center gap-1"><Link2 class="h-3 w-3" />{t('resources.memory.sourceExplanation', { count: sourceCount })}</span>
          {/if}
        </span>
      </button>
      <Button
        variant="ghost"
        size="icon"
        ariaLabel={t('resources.memory.viewDetail')}
        title={t('resources.memory.viewDetail')}
        onclick={() => openMemoryDetail(record.memory_id)}
      >
        <Eye class="h-3.5 w-3.5" />
      </Button>
    </div>
  </article>
{/snippet}

{#snippet settingsField(label: TranslationKey, value: string)}
  <div class="border-b border-border px-1 py-3 sm:odd:border-r sm:px-3">
    <dt class="text-[11px] text-muted-foreground">{t(label)}</dt>
    <dd class="mt-1 break-words text-[13px] font-medium text-foreground">{displayField(value)}</dd>
  </div>
{/snippet}

{#snippet detailRow(label: TranslationKey, value: string)}
  <div class="border-b border-border py-3 sm:odd:border-r sm:odd:pr-4 sm:even:pl-4">
    <dt class="text-[11px] text-muted-foreground">{t(label)}</dt>
    <dd class="mt-1 break-words text-[13px] font-medium text-foreground">{displayField(value)}</dd>
  </div>
{/snippet}

{#snippet textBlock(label: TranslationKey, value: string | undefined)}
  {#if value}
    <section>
      <h3 class="text-xs font-medium text-muted-foreground">{t(label)}</h3>
      <p class="mt-2 whitespace-pre-wrap text-sm leading-6 text-foreground">{value}</p>
    </section>
  {/if}
{/snippet}

{#snippet detailList(label: TranslationKey, values: string[])}
  {#if values.length > 0}
    <section>
      <h3 class="text-xs font-medium text-muted-foreground">{t(label)}</h3>
      <ul class="mt-2 space-y-1 text-sm leading-6 text-foreground">
        {#each values as value}
          <li>{value}</li>
        {/each}
      </ul>
    </section>
  {/if}
{/snippet}
