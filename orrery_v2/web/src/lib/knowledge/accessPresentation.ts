import type { KnowledgeBaseDTO } from '#lib/types/api/knowledge'

export function websiteKnowledgeAudience(base?: KnowledgeBaseDTO): 'public' | 'customer' | undefined {
  if (base?.provider !== 'pageindex') return undefined
  if (base.name === 'orrery-public') return 'public'
  if (base.name === 'website-customer') return 'customer'
  return undefined
}

export function knowledgeAccessLabel(base: KnowledgeBaseDTO): string {
  const audience = websiteKnowledgeAudience(base)
  if (audience === 'public') return '网站公开资料'
  if (audience === 'customer') return '已验证客户资料'
  if (base.visibility === 'private') return '个人知识库'
  if (base.visibility === 'scoped') return '按授权访问'
  return '组织内共享'
}

export function websiteKnowledgeConsumers(base?: KnowledgeBaseDTO): string[] {
  const audience = websiteKnowledgeAudience(base)
  if (audience === 'public') return ['网站访客咨询 · MAX', '网站客户咨询', '网站知识维护 · 只读检索']
  if (audience === 'customer') return ['网站客户咨询 · 需验证客户身份']
  return []
}
