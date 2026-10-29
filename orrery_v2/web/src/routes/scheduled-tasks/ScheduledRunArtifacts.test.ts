import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'

const { readRunArtifactMock, createObjectURLMock, revokeObjectURLMock } = vi.hoisted(() => ({
  readRunArtifactMock: vi.fn(),
  createObjectURLMock: vi.fn(() => 'blob:artifact-download'),
  revokeObjectURLMock: vi.fn()
}))

vi.mock('#lib/api/scheduler', () => ({
  schedulerApi: { readRunArtifact: readRunArtifactMock }
}))

import ScheduledRunArtifacts from './ScheduledRunArtifacts.svelte'

describe('ScheduledRunArtifacts', () => {
  beforeEach(() => {
    readRunArtifactMock.mockReset()
    createObjectURLMock.mockClear()
    revokeObjectURLMock.mockClear()
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURLMock })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURLMock })
    setLocale('zh-CN')
  })

  it('shows safe artifact metadata and previews markdown', async () => {
    readRunArtifactMock.mockResolvedValue({
      type: 'text/markdown; charset=utf-8',
      text: async () => '# Weekly report'
    } as Blob)
    render(ScheduledRunArtifacts, {
      props: {
        scheduledTaskID: 'task-1',
        scheduledRunID: 'run-1',
        artifacts: [{
          id: 'artifact-1',
          kind: 'report',
          file_name: 'weekly-report.md',
          size_bytes: 1536,
          previewable: true,
          content_available: true,
          content_availability_checked: true
        }]
      }
    })

    expect(screen.getByText('weekly-report.md')).toBeTruthy()
    expect(screen.getByText('1.5 KB')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: '预览 weekly-report.md' }))

    await waitFor(() => expect(screen.getByRole('heading', { name: 'Weekly report' })).toBeTruthy())
    expect(readRunArtifactMock).toHaveBeenCalledWith('task-1', 'run-1', 'artifact-1', 'inline')
  })

  it('downloads through the streaming endpoint', async () => {
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    readRunArtifactMock.mockResolvedValue(new Blob(['# weekly'], { type: 'text/markdown' }))
    render(ScheduledRunArtifacts, {
      props: {
        scheduledTaskID: 'task-1',
        scheduledRunID: 'run-1',
        artifacts: [{ id: 'report/weekly', kind: 'report', file_name: 'weekly.md', previewable: false, content_available: true, content_availability_checked: true }]
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: '下载 weekly.md' }))

    await waitFor(() => expect(readRunArtifactMock).toHaveBeenCalledWith('task-1', 'run-1', 'report/weekly', 'attachment'))
    expect(createObjectURLMock).toHaveBeenCalledOnce()
    expect(revokeObjectURLMock).toHaveBeenCalledWith('blob:artifact-download')
    expect(click).toHaveBeenCalledOnce()
    click.mockRestore()
  })

  it('shows unavailable historical metadata without content actions', () => {
    render(ScheduledRunArtifacts, {
      props: {
        scheduledTaskID: 'task-1',
        scheduledRunID: 'run-old',
        artifacts: [{
          id: 'artifact-old',
          kind: 'report',
          file_name: 'historical-report.md',
          previewable: true,
          content_available: false,
          content_availability_checked: true
        }]
      }
    })

    expect(screen.getByText('内容不可用')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '预览 historical-report.md' })).toBeNull()
    expect(screen.queryByRole('button', { name: '下载 historical-report.md' })).toBeNull()
    expect(readRunArtifactMock).not.toHaveBeenCalled()
  })

  it('keeps content actions available when the list budget leaves an artifact unchecked', () => {
	setLocale('en-US')
    render(ScheduledRunArtifacts, {
      props: {
        scheduledTaskID: 'task-1',
        scheduledRunID: 'run-unchecked',
        artifacts: [{
          id: 'artifact-unchecked',
          kind: 'report',
          file_name: 'unchecked.md',
          previewable: true,
          content_available: false,
          content_availability_checked: false
        }]
      }
    })

    expect(screen.queryByText('Content unavailable')).toBeNull()
    expect(screen.getByRole('button', { name: 'Preview unchecked.md' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Download unchecked.md' })).toBeTruthy()
  })

  it('ignores a stale preview response after another artifact is selected', async () => {
	setLocale('en-US')
    let resolveA!: (value: Blob) => void
    let resolveB!: (value: Blob) => void
    readRunArtifactMock.mockImplementation((_taskID: string, _runID: string, artifactID: string) => new Promise<Blob>((resolve) => {
      if (artifactID === 'artifact-a') resolveA = resolve
      else resolveB = resolve
    }))
    render(ScheduledRunArtifacts, {
      props: {
        scheduledTaskID: 'task-1',
        scheduledRunID: 'run-1',
        artifacts: [
          { id: 'artifact-a', kind: 'report', file_name: 'a.md', previewable: true, content_available: true, content_availability_checked: true },
          { id: 'artifact-b', kind: 'report', file_name: 'b.md', previewable: true, content_available: true, content_availability_checked: true }
        ]
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Preview a.md' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Preview b.md' }))
    resolveB({ type: 'text/markdown', text: async () => '# Result B' } as Blob)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Result B' })).toBeTruthy())
    resolveA({ type: 'text/markdown', text: async () => '# Result A' } as Blob)
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Result A' })).toBeNull())
    expect(screen.getByRole('heading', { name: 'Result B' })).toBeTruthy()
  })
})
