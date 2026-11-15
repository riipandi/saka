import { beforeEach, describe, expect, it } from 'vite-plus/test'
import { appStore, readStoredClosedGroups, setSidebarGroupOpen } from '#/libraries/app.store'

const KEY = 'app.sidebarClosedGroups'

describe('the sidebar group preference', () => {
  beforeEach(() => {
    window.localStorage.clear()
    appStore.setState((prev) => ({ ...prev, sidebarClosedGroups: [] }))
  })

  it('remembers a folded group and forgets it when it opens again', () => {
    setSidebarGroupOpen('Platform', false)
    expect(appStore.state.sidebarClosedGroups).toEqual(['Platform'])
    expect(readStoredClosedGroups()).toEqual(['Platform'])

    setSidebarGroupOpen('Platform', true)
    expect(appStore.state.sidebarClosedGroups).toEqual([])
    expect(readStoredClosedGroups()).toEqual([])
  })

  it('folds a group once however often it is folded', () => {
    setSidebarGroupOpen('Platform', false)
    setSidebarGroupOpen('Platform', false)
    expect(appStore.state.sidebarClosedGroups).toEqual(['Platform'])
  })

  it('opens every group when the stored value is not a list of labels', () => {
    for (const stored of ['not json', '{"a":1}', '[1,2]', 'null']) {
      window.localStorage.setItem(KEY, stored)
      expect(readStoredClosedGroups()).toEqual([])
    }
  })
})
