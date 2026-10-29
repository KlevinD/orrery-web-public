import {
  BarChart3,
  Boxes,
  BrainCircuit,
  CheckSquare,
  FileSearch,
  GitBranch,
  KeyRound,
  Library,
  LayoutDashboard,
  ListTodo,
  MessageSquareText,
  Network,
  Rocket,
  Server,
  Settings,
  UserCircle,
  Users,
  Workflow,
  Wrench
} from 'lucide-svelte'
import type { TranslationKey } from '#lib/i18n'
import { hasAllPermissionScopes } from '#lib/permissions/access'
import { isLegacyRouteAliasDisabled } from '#lib/routes/legacy-route-aliases'

export interface RoutePage {
  path: string
  title: TranslationKey
  description: TranslationKey
}

export interface NavItem extends RoutePage {
  icon: typeof Rocket
  badge?: string
}

export interface NavGroup {
  title: TranslationKey
  items: NavItem[]
}

export interface RouteAccessRequirement {
  allOf?: readonly string[]
  anyOf?: readonly string[]
}

export const routePages: RoutePage[] = [
  { path: '/', title: 'nav.home', description: 'nav.home.description' },
  { path: '/work-items', title: 'nav.workItems', description: 'nav.workItems.description' },
  { path: '/discussions', title: 'nav.discussions', description: 'nav.discussions.description' },
  { path: '/missions', title: 'nav.missionList', description: 'nav.missionList.description' },
  { path: '/responsibilities', title: 'nav.responsibilities', description: 'nav.responsibilities.description' },
  { path: '/scheduled-tasks', title: 'nav.scheduler', description: 'nav.scheduler.description' },
  { path: '/resources/tools', title: 'nav.tools', description: 'nav.tools.description' },
  { path: '/resources/skills', title: 'nav.skills', description: 'nav.skills.description' },
  { path: '/resources/mcp', title: 'nav.mcp', description: 'nav.mcp.description' },
  { path: '/resources/memory', title: 'nav.memoryResources', description: 'nav.memoryResources.description' },
  { path: '/resources/ontology', title: 'nav.ontology', description: 'nav.ontology.description' },
  { path: '/resources/knowledge', title: 'nav.knowledge', description: 'nav.knowledge.description' },
  { path: '/approvals', title: 'nav.approvals', description: 'nav.approvals.description' },
  { path: '/approvals/workflows', title: 'nav.approvalWorkflows', description: 'nav.approvalWorkflows.description' },
  { path: '/permissions', title: 'nav.permissions', description: 'nav.permissions.description' },
  { path: '/people', title: 'nav.people', description: 'nav.people.description' },
  { path: '/org', title: 'nav.org', description: 'nav.org.description' },
  { path: '/squads', title: 'nav.squads', description: 'nav.squads.description' },
  { path: '/evaluation', title: 'nav.evaluation', description: 'nav.evaluation.description' },
  { path: '/audit', title: 'nav.audit', description: 'nav.audit.description' },
  { path: '/settings', title: 'nav.settings', description: 'nav.settings.description' },
  { path: '/settings/scm-connections', title: 'nav.scmConnections', description: 'nav.scmConnections.description' },
  { path: '/profile', title: 'profile.title', description: 'profile.description' }
]

const byPath = new Map(routePages.map((page) => [page.path, page]))

export const navGroups: NavGroup[] = [
  {
    title: 'nav.group.work',
    items: [
      { ...byPath.get('/')!, icon: LayoutDashboard },
      { ...byPath.get('/discussions')!, icon: MessageSquareText },
      { ...byPath.get('/work-items')!, icon: ListTodo },
      { ...byPath.get('/scheduled-tasks')!, icon: Workflow }
    ]
  },
  {
    title: 'nav.group.people',
    items: [
      { ...byPath.get('/people')!, icon: Users },
      { ...byPath.get('/org')!, icon: Network }
    ]
  },
  {
    title: 'nav.group.control',
    items: [
      { ...byPath.get('/permissions')!, icon: KeyRound },
      { ...byPath.get('/audit')!, icon: FileSearch },
      { ...byPath.get('/evaluation')!, icon: BarChart3 }
    ]
  },
  {
    title: 'nav.group.resources',
    items: [
      { ...byPath.get('/resources/tools')!, icon: Wrench },
      { ...byPath.get('/resources/skills')!, icon: Boxes },
      { ...byPath.get('/resources/mcp')!, icon: Server },
      { ...byPath.get('/resources/memory')!, icon: BrainCircuit },
      { ...byPath.get('/resources/ontology')!, icon: GitBranch },
      { ...byPath.get('/resources/knowledge')!, icon: Library }
    ]
  },
  {
    title: 'nav.group.account',
    items: [
      { ...byPath.get('/settings')!, icon: Settings },
      { ...byPath.get('/settings/scm-connections')!, icon: GitBranch },
      { ...byPath.get('/profile')!, icon: UserCircle }
    ]
  }
]

export const routeAccessRequirements: Readonly<Record<string, readonly string[] | RouteAccessRequirement>> = {
  '/': ['todo.read', 'idea.read'],
  '/work-items': { anyOf: ['todo.read', 'idea.read', 'mission.read', 'approvals.read', 'scheduler.read'] },
  '/discussions': ['discussion.read'],
  '/discussion': ['discussion.read', 'mission.read'],
  '/mission-room': ['mission.read'],
  '/missions': ['mission.read'],
  '/responsibilities': { anyOf: ['operations.read', 'operations.manage'] },
  '/scheduled-tasks': ['scheduler.read'],
  '/resources/tools': ['resource.read'],
  '/resources/skills': ['resource.read'],
  '/resources/mcp': ['resource.read'],
  '/resources/memory': ['resource.read'],
  '/resources/ontology': ['operations.read'],
  '/resources/knowledge/new': ['knowledge.manage'],
  '/resources/knowledge': ['knowledge.read'],
  '/approvals': ['approvals.read'],
  '/approvals/workflows': ['approvals.read'],
  '/permissions': ['permissions.read', 'roles.read'],
  '/people': ['workforce.read', 'org.read', 'roles.read'],
  '/org': ['org.read'],
  '/squads': ['org.read'],
  '/evaluation': ['operations.read'],
  '/audit': ['audit.read'],
  '/memory': ['resource.read'],
  '/settings': ['settings.read'],
  '/settings/approval-workflows': ['approvals.read'],
  '/personal-assistant': [],
  '/profile': [],
  '/todos': ['todo.read'],
  '/ideas': ['idea.read'],
  '/settings/scm-connections': ['connection.read']
}

const routeAccessEntries = Object.entries(routeAccessRequirements)
  .filter(([path]) => path !== '/')
  .sort(([left], [right]) => right.length - left.length)

export function hasRouteAccess(pathname: string, scopes: readonly string[]) {
  if (isLegacyRouteAliasDisabled(pathname)) return false
  const required = accessRequirementsForPath(pathname)
  if (required === undefined) return false
  if (Array.isArray(required)) return hasAllPermissionScopes(scopes, required)

  const requirement = required as RouteAccessRequirement
  const allOf = requirement.allOf ?? []
  const anyOf = requirement.anyOf ?? []
  return hasAllPermissionScopes(scopes, allOf) &&
    (anyOf.length === 0 || anyOf.some((scope) => hasAllPermissionScopes(scopes, [scope])))
}

export function visibleNavGroups(scopes: readonly string[]) {
  return navGroups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => hasRouteAccess(item.path, scopes))
    }))
    .filter((group) => group.items.length > 0)
}

function accessRequirementsForPath(pathname: string) {
  const normalized = normalizePath(pathname)
  if (normalized === '/') return routeAccessRequirements['/']

  for (const [path, required] of routeAccessEntries) {
    if (normalized === path || normalized.startsWith(`${path}/`)) {
      return required
    }
  }

  return undefined
}

function normalizePath(pathname: string) {
  const trimmed = pathname.trim()
  if (!trimmed || trimmed === '/') return '/'
  return `/${trimmed.replace(/^\/+|\/+$/g, '')}`
}

export function pageForPath(pathname: string): RoutePage {
  const normalized = normalizePath(pathname)
  if (normalized.startsWith('/missions/') && normalized !== '/missions') {
    return {
      path: normalized,
      title: 'nav.missionWorkspace',
      description: 'nav.missionWorkspace.description'
    }
  }
  if (normalized.startsWith('/discussion/')) {
    return {
      path: normalized,
      title: 'nav.discussion',
      description: 'nav.discussion.description'
    }
  }
  if (normalized.startsWith('/todos/')) {
    return {
      path: normalized,
      title: 'workhub.detail.task',
      description: 'nav.home.description'
    }
  }
  if (normalized.startsWith('/ideas/')) {
    return {
      path: normalized,
      title: 'workhub.detail.idea',
      description: 'nav.home.description'
    }
  }
  const exact = byPath.get(normalized)
  if (exact) return exact

  const parent = routePages
    .filter((page) => page.path !== '/' && normalized.startsWith(`${page.path}/`))
    .sort((left, right) => right.path.length - left.path.length)[0]
  if (parent) return { ...parent, path: normalized }

  return byPath.get('/')!
}
