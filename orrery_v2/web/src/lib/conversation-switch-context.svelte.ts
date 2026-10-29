export type ConversationSwitchItem = {
  id: string
  title: string
  agentName: string
}

interface ConversationSwitchContextState {
  available: boolean
  conversations: ConversationSwitchItem[]
  selectedConversationId: string
  onSelect?: (conversation: ConversationSwitchItem) => void
}

export const conversationSwitchContext = $state<ConversationSwitchContextState>({
  available: false,
  conversations: [],
  selectedConversationId: '',
  onSelect: undefined
})

export function updateConversationSwitchContext(input: Omit<ConversationSwitchContextState, 'available'>) {
  conversationSwitchContext.available = true
  conversationSwitchContext.conversations = input.conversations
  conversationSwitchContext.selectedConversationId = input.selectedConversationId
  conversationSwitchContext.onSelect = input.onSelect
}

export function clearConversationSwitchContext() {
  conversationSwitchContext.available = false
  conversationSwitchContext.conversations = []
  conversationSwitchContext.selectedConversationId = ''
  conversationSwitchContext.onSelect = undefined
}
