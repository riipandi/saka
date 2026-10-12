import * as stylex from '@stylexjs/stylex'
import ct from 'countries-and-timezones'
import type { Timezone } from 'countries-and-timezones'
import { useMemo } from 'react'
import {
  Combobox,
  ComboboxCollection,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxGroup,
  ComboboxInput,
  ComboboxItem,
  ComboboxLabel,
  ComboboxList
} from 'uilibs/components/base/combobox'

/** The one special row: an empty preference means UTC — the server's
 * `DefaultTimezone` — so the picker opens with it instead of forcing the
 * account to find one zone among hundreds. */
export const NO_TIMEZONE = '__none__'

interface TimezoneOption {
  name: string
  offset: string
  country: string
  countryCode: string
}

interface TimezoneGroup {
  countryCode: string
  countryName: string
  options: TimezoneOption[]
}

/** The full IANA zone list the library ships, grouped by the country each
 * zone is primarily used in, sorted by country name then zone name, each
 * row labeled with its offset — the list a human can actually scan. */
const GROUPS: TimezoneGroup[] = buildGroups()

function buildGroups(): TimezoneGroup[] {
  const zones = ct.getAllTimezones({ deprecated: false }) as Record<string, Timezone>
  const countries = ct.getAllCountries() as Record<string, { id: string; name: string }>

  const byCountry = new Map<string, TimezoneOption[]>()
  for (const zone of Object.values(zones)) {
    // The offset string is the row's second word — a zone name alone says
    // geography, not time. The `Etc/*` block stays out: GMT±N offsets are
    // the server's world, not an account's; the UTC row covers the zero.
    if (zone.name.startsWith('Etc/')) continue
    const countryCode = zone.countries[0] ?? ''
    const entry: TimezoneOption = {
      name: zone.name,
      offset: zone.utcOffsetStr,
      country: countries[countryCode]?.name ?? countryCode,
      countryCode
    }
    const bucket = byCountry.get(countryCode)
    if (bucket) bucket.push(entry)
    else byCountry.set(countryCode, [entry])
  }

  return [...byCountry.entries()]
    .map(([countryCode, options]) => ({
      countryCode,
      countryName: countries[countryCode]?.name ?? (countryCode || 'UTC and elsewhere'),
      options: options.toSorted((a, b) => a.name.localeCompare(b.name))
    }))
    .toSorted((a, b) => a.countryName.localeCompare(b.countryName))
}

/** The browser's own zone, offered as the picker's shortcut — an account
 * answering from the machine it sits at should find it one select away. */
export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    return ''
  }
}

/**
 * The IANA zone picker: a searchable combobox grouped by country, each
 * row carrying its UTC offset, the browser's own zone preselected when
 * the account holds no preference. The value is the zone's IANA name —
 * the wire word `normalizeTimezone` judges — and clearing the choice
 * sends the empty string the server reads as UTC.
 */
export function TimezonePicker({
  value,
  onValueChange,
  disabled
}: {
  value: string
  onValueChange: (next: string) => void
  disabled?: boolean
}) {
  const groups = useMemo(() => {
    if (!value || value === NO_TIMEZONE) return GROUPS
    // The chosen zone rides the top as its own group, so the current
    // answer stays visible while the rest of the world scrolls under it.
    const rest = GROUPS.map((group) => ({
      ...group,
      options: group.options.filter((option) => option.name !== value)
    })).filter((group) => group.options.length > 0)
    const chosen = GROUPS.flatMap((group) => group.options).find((option) => option.name === value)
    if (!chosen) return rest
    return [
      {
        countryCode: '',
        countryName: 'Current preference',
        options: [chosen]
      },
      ...rest
    ]
  }, [value])

  return (
    <Combobox
      items={groups}
      value={value || NO_TIMEZONE}
      onValueChange={(next: string | null) => {
        onValueChange(next && next !== NO_TIMEZONE ? next : '')
      }}
      disabled={disabled}
    >
      <ComboboxInput placeholder='e.g. Asia/Jakarta' />
      <ComboboxContent>
        <ComboboxEmpty>No zone matches that name.</ComboboxEmpty>
        <ComboboxList>
          {(group: TimezoneGroup) => (
            <ComboboxGroup key={group.countryCode || 'utc'} items={group.options}>
              <ComboboxLabel>{group.countryName}</ComboboxLabel>
              <ComboboxCollection>
                {(option: TimezoneOption) => (
                  <ComboboxItem key={option.name} value={option.name}>
                    <span {...stylex.props(styles.option)}>
                      <span {...stylex.props(styles.mono)}>{option.offset}</span>
                      {option.name}
                    </span>
                  </ComboboxItem>
                )}
              </ComboboxCollection>
            </ComboboxGroup>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}

const styles = stylex.create({
  option: {
    display: 'flex',
    alignItems: 'center',
    gap: 8
  },
  mono: {
    fontFamily: 'var(--font-monospace, monospace)',
    fontSize: 12,
    color: 'var(--color-neutral-faded, #666)'
  }
})

// Re-exported so the settings view's "clear" affordance shares the word.
export { NO_TIMEZONE as noTimezoneValue }
