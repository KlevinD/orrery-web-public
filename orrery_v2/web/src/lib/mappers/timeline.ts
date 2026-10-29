import type { DiscussionDTO } from '#lib/types/api'
import type { DiscussionViewModel } from '#lib/types/view-models'

export function mapDiscussionView(dto: DiscussionDTO): DiscussionViewModel {
  return {
    groupId: dto.discussion_group_id,
    messages: (dto.messages ?? []).map((message) => ({
      id: message.message_id,
      groupId: message.discussion_group_id,
      kind: message.kind || 'message',
      authorId: message.author_id,
      authorName: message.author_display_name || message.author_id,
      authorType: message.author_type || 'Not provided',
      body: message.body,
      mentions: message.mentions ?? [],
      replyToMessageId: message.reply_to_message_id || '',
      replyToAuthor: '',
      replyPreview: message.reply_preview || '',
      toolName: message.metadata?.tool_name || '',
      oldStatus: message.metadata?.old_status || '',
      newStatus: message.metadata?.new_status || '',
      postedAt: formatDate(message.posted_at)
    })),
    pendingMentions: dto.pending_mentions?.length ?? 0,
    decisions: dto.decisions?.length ?? 0,
    blockers: dto.blockers?.length ?? 0
  }
}

function formatDate(value: string | undefined) {
  if (!value) return 'Not provided'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return new Intl.DateTimeFormat('en', { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(parsed)
}
