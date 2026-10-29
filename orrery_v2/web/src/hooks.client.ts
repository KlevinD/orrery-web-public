import type { HandleClientError } from '@sveltejs/kit/hooks'
import { recoverFromChunkLoadError } from '#lib/runtime/chunkRecovery'
import { reportClientError } from '#lib/runtime/error-reporting'

export const handleError: HandleClientError = ({ error }) => {
  if (recoverFromChunkLoadError(error)) {
    return { message: 'The application was updated. Reloading the current version.' }
  }

  console.error(error)
  reportClientError('javascript', error)
  const safeMessage = typeof error === 'object' && error !== null && 'message' in error && typeof error.message === 'string'
    ? error.message
    : 'Internal Error'
  return { message: safeMessage }
}
