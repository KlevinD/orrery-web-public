import { describe, expect, it } from 'vitest'
import { parseDiscussionMentionLinks, renderPlainDiscussionMentions, replaceDiscussionMentionLinks } from './discussionMentions'

describe('discussion mention links', () => {
  it('parses valid links without accepting malformed routing targets', () => {
    expect(parseDiscussionMentionLinks(
      '[broken [@Lead](mention://agent/lead-agent) [@User](mention://user/user-1) [@Bad](mention://agent/id with spaces)'
    )).toMatchObject([
      { kind: 'agent', id: 'lead-agent', label: 'Lead' },
      { kind: 'user', id: 'user-1', label: 'User' }
    ])
  })

  it('renders display text while preserving surrounding content', () => {
    expect(renderPlainDiscussionMentions('Ask [@Lead](mention://agent/lead-agent), then [User](mention://user/user-1).'))
      .toBe('Ask @Lead, then @User.')
  })

  it('replaces links in source order', () => {
    expect(replaceDiscussionMentionLinks(
      '[@A](mention://agent/a) [@B](mention://agent/b)',
      (link, index) => `${index}:${link.id}`
    )).toBe('0:a 1:b')
  })

  it('parses and renders bare provider mention links', () => {
    const content = '已安排执行。\n\nmention://agent/profile-f54c4401bfb058eadd0e48a5a878b4e4 请负责交付。'
    expect(parseDiscussionMentionLinks(content)).toMatchObject([
      { kind: 'agent', id: 'profile-f54c4401bfb058eadd0e48a5a878b4e4' }
    ])
    expect(renderPlainDiscussionMentions(content, () => '前端开发 Agent'))
      .toBe('已安排执行。\n\n@前端开发 Agent 请负责交付。')
  })

  it('does not reinterpret malformed markdown mentions as bare links', () => {
    expect(parseDiscussionMentionLinks('[@Bad](mention://agent/id with spaces)')).toEqual([])
  })
})
