declare global {
  namespace App {
    interface PageState {
      discussionComposer?: {
        draft: string
        sendImmediately?: boolean
        directTargetId?: string
        directTargetKind?: 'agent' | 'user'
        directTargetName?: string
      }
    }
  }
}

export {}
