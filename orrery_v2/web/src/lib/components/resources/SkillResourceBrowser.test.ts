import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import SkillResourceBrowser from './SkillResourceBrowser.svelte'
import type { SkillFileDTO, SkillFileRootDTO } from '#lib/types/api'

describe('SkillResourceBrowser', () => {
  it('keeps package roots separated and previews text files when clicked', async () => {
    const onpreview = vi.fn().mockResolvedValue(textFileFixture())
    setLocale('en-US')
    render(SkillResourceBrowser, {
      props: {
        roots: rootsFixture(),
        editable: false,
        selectedFile: undefined,
        busyPath: '',
        onpreview,
        ondownload: vi.fn(),
        onsave: vi.fn(),
        onupload: vi.fn(),
        oncreatefolder: vi.fn()
      }
    })

    expect(within(screen.getByLabelText('Assets root')).getByText('logo.png')).toBeTruthy()
    expect(within(screen.getByLabelText('Assets root')).getByText('3')).toBeTruthy()
    expect(within(screen.getByLabelText('Resources root')).getByText('guide.md')).toBeTruthy()
    expect(within(screen.getByLabelText('References root')).getByText('paper.txt')).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Preview resources/guide.md' }))

    expect(onpreview).toHaveBeenCalledWith('resources/guide.md')
  })

  it('downloads files that cannot be previewed when clicked', async () => {
    const ondownload = vi.fn()
    setLocale('en-US')
    render(SkillResourceBrowser, {
      props: {
        roots: rootsFixture(),
        editable: false,
        selectedFile: undefined,
        busyPath: '',
        onpreview: vi.fn(),
        ondownload,
        onsave: vi.fn(),
        onupload: vi.fn(),
        oncreatefolder: vi.fn()
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Open assets/logo.png' }))

    expect(ondownload).toHaveBeenCalledWith('assets/logo.png')
  })

  it('renders image previews from authenticated blob URLs instead of raw API URLs', () => {
    setLocale('en-US')
    render(SkillResourceBrowser, {
      props: {
        roots: rootsFixture(),
        editable: false,
        selectedFile: imageFileFixture(),
        imagePreviewUrl: 'blob:skill-image-preview',
        busyPath: '',
        onpreview: vi.fn(),
        ondownload: vi.fn(),
        onsave: vi.fn(),
        onupload: vi.fn(),
        oncreatefolder: vi.fn()
      }
    })

    const preview = screen.getByRole('img', { name: 'logo.png' })

    expect(preview.getAttribute('src')).toBe('blob:skill-image-preview')
    expect(preview.getAttribute('src')).not.toBe('/api/resources/skills/skill.review/files/download?path=assets%2Flogo.png')
  })

  it('treats API null root children as an empty resource group', () => {
    setLocale('en-US')
    render(SkillResourceBrowser, {
      props: {
        roots: [
          {
            name: 'scripts',
            label: 'Scripts',
            exists: false,
            children: null as unknown as SkillFileRootDTO['children']
          }
        ],
        editable: false,
        selectedFile: undefined,
        busyPath: '',
        onpreview: vi.fn(),
        ondownload: vi.fn(),
        onsave: vi.fn(),
        onupload: vi.fn(),
        oncreatefolder: vi.fn()
      }
    })

    const root = screen.getByLabelText('Scripts root')

    expect(within(root).getByText('0')).toBeTruthy()
    expect(within(root).getByText('No files in this root.')).toBeTruthy()
  })

  it('edits selected text resources and exposes upload rows at the end of each root', async () => {
    const onsave = vi.fn()
    const onupload = vi.fn()
    const ondownload = vi.fn()
    setLocale('en-US')
    render(SkillResourceBrowser, {
      props: {
        roots: rootsFixture(),
        editable: true,
        selectedFile: textFileFixture(),
        busyPath: '',
        onpreview: vi.fn(),
        ondownload,
        onsave,
        onupload
      }
    })

    await fireEvent.input(screen.getByLabelText('Resource source'), { target: { value: 'updated guide' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save resource' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Download selected resources/guide.md' }))
    const upload = within(screen.getByLabelText('Assets root')).getByLabelText('Upload to assets')
    const file = new File(['new logo'], 'new-logo.png', { type: 'image/png' })
    await fireEvent.change(upload, { target: { files: [file] } })

    expect(onsave).toHaveBeenCalledWith('resources/guide.md', 'updated guide')
    expect(ondownload).toHaveBeenCalledWith('resources/guide.md')
    expect(onupload).toHaveBeenCalledWith('assets', file)
    expect(within(screen.getByLabelText('Assets root')).getByLabelText('Upload to assets')).toBeTruthy()
    expect(within(screen.getByLabelText('Resources root')).getByLabelText('Upload to resources')).toBeTruthy()
  })

  it('creates folders from the end of an editable root section', async () => {
    const oncreatefolder = vi.fn()
    setLocale('en-US')
    render(SkillResourceBrowser, {
      props: {
        roots: rootsFixture(),
        editable: true,
        selectedFile: undefined,
        busyPath: '',
        onpreview: vi.fn(),
        ondownload: vi.fn(),
        onsave: vi.fn(),
        onupload: vi.fn(),
        oncreatefolder
      }
    })

    const root = within(screen.getByLabelText('Resources root'))
    await fireEvent.input(root.getByLabelText('New folder in resources'), { target: { value: 'drafts' } })
    await fireEvent.click(root.getByRole('button', { name: 'Create folder in resources' }))

    expect(oncreatefolder).toHaveBeenCalledWith('resources', 'drafts')
  })
})

function rootsFixture(): SkillFileRootDTO[] {
  return [
    {
      name: 'assets',
      label: 'Assets',
      exists: true,
      children: [
        {
          path: 'assets/logo.png',
          name: 'logo.png',
          type: 'file',
          size: 2048,
          media_type: 'image/png',
          preview_kind: 'none'
        },
        {
          path: 'assets/icons',
          name: 'icons',
          type: 'directory',
          size: 0,
          media_type: '',
          preview_kind: 'none',
          children: [
            {
              path: 'assets/icons/check.svg',
              name: 'check.svg',
              type: 'file',
              size: 256,
              media_type: 'image/svg+xml',
              preview_kind: 'image'
            },
            {
              path: 'assets/icons/close.svg',
              name: 'close.svg',
              type: 'file',
              size: 256,
              media_type: 'image/svg+xml',
              preview_kind: 'image'
            }
          ]
        }
      ]
    },
    {
      name: 'resources',
      label: 'Resources',
      exists: true,
      children: [
        {
          path: 'resources/guide.md',
          name: 'guide.md',
          type: 'file',
          size: 128,
          media_type: 'text/markdown',
          preview_kind: 'markdown'
        }
      ]
    },
    {
      name: 'references',
      label: 'References',
      exists: true,
      children: [
        {
          path: 'references/paper.txt',
          name: 'paper.txt',
          type: 'file',
          size: 64,
          media_type: 'text/plain',
          preview_kind: 'text'
        }
      ]
    }
  ]
}

function textFileFixture(): SkillFileDTO {
  return {
    node: {
      path: 'resources/guide.md',
      name: 'guide.md',
      type: 'file',
      size: 128,
      media_type: 'text/markdown',
      preview_kind: 'markdown'
    },
    path: 'resources/guide.md',
    name: 'guide.md',
    size: 128,
    media_type: 'text/markdown',
    preview_kind: 'markdown',
    content: '# Guide'
  }
}

function imageFileFixture(): SkillFileDTO {
  return {
    node: {
      path: 'assets/logo.png',
      name: 'logo.png',
      type: 'file',
      size: 2048,
      media_type: 'image/png',
      preview_kind: 'image',
      download_url: '/api/resources/skills/skill.review/files/download?path=assets%2Flogo.png'
    },
    path: 'assets/logo.png',
    name: 'logo.png',
    size: 2048,
    media_type: 'image/png',
    preview_kind: 'image',
    download_url: '/api/resources/skills/skill.review/files/download?path=assets%2Flogo.png'
  }
}
