import { Search } from '@keyline-icons/react'
import { useRouter } from '@tanstack/react-router'
import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { Button } from 'uilibs/components/base/button'
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
  CommandShortcut
} from 'uilibs/components/extra/command'
import { Kbd } from 'uilibs/components/extra/kbd'
import { usePermissions } from '#/libraries/guard/use-permissions'
import { adminGroups, admitsAdminNav } from './-sidebar'
import { accountLinks } from './-user-menu'

interface Destination {
  value: string
  label: string
  group: string
  to: string
}

const itemLabel = (item: unknown): string =>
  typeof item === 'object' && item !== null && 'label' in item ? String(item.label) : ''

const OpenCommandContext = createContext<(() => void) | null>(null)

/** ⌘K (Ctrl+K elsewhere) jumps to a page the viewer is allowed to open. Mount once. */
export function CommandPalette({ children }: { children: ReactNode }) {
  const router = useRouter()
  const { can, isAdministrator } = usePermissions()
  const [open, setOpen] = useState(false)

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setOpen((current) => !current)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const destinations: Destination[] = [
    { value: '/overview', label: 'Overview', group: 'Core', to: '/overview' },
    ...adminGroups.flatMap((group) =>
      group.items
        .filter((item) => item.href !== undefined && admitsAdminNav(item, can, isAdministrator))
        .map((item) => ({
          value: String(item.href),
          label: item.label,
          group: group.label,
          to: String(item.href)
        }))
    ),
    ...accountLinks.flatMap((link) =>
      link.to === undefined
        ? []
        : [{ value: link.to, label: link.label, group: 'Account', to: link.to }]
    )
  ]

  const go = (to: string) => {
    setOpen(false)
    void router.navigate({ to })
  }

  return (
    <OpenCommandContext.Provider value={() => setOpen(true)}>
      {children}
      <CommandDialog
        open={open}
        onOpenChange={setOpen}
        title='Jump to a page'
        description='Search the pages you can open.'
      >
        <Command items={destinations} itemToStringValue={itemLabel}>
          <CommandInput placeholder='Jump to a page…' aria-label='Jump to a page' />
          <CommandEmpty>No pages match.</CommandEmpty>
          <CommandList>
            {(item: Destination) => (
              <CommandItem key={item.value} value={item} onClick={() => go(item.to)}>
                {item.label}
                <CommandShortcut>{item.group}</CommandShortcut>
              </CommandItem>
            )}
          </CommandList>
        </Command>
      </CommandDialog>
    </OpenCommandContext.Provider>
  )
}

/** The header trigger. The dialog and the shortcut live on CommandPalette, mounted once. */
export function CommandPaletteButton() {
  const onOpen = useContext(OpenCommandContext)
  return (
    <Button
      variant='outline'
      size='sm'
      onClick={() => onOpen?.()}
      aria-label='Open command palette'
    >
      <Search size={16} aria-hidden />
      Search
      <Kbd>⌘K</Kbd>
    </Button>
  )
}
