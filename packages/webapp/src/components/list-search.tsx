import { Search } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import type { FormEvent } from 'react'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput
} from 'uilibs/components/extra/input-group'
import { Kbd } from 'uilibs/components/extra/kbd'

interface ListSearchProps {
  value: string
  onValueChange: (value: string) => void
  /** The trimmed field. An empty string clears the filter. */
  onSubmit: (value: string) => void
  placeholder: string
  label: string
  style?: stylex.StyleXStyles
}

/** The list filter: a search glyph, the field, and clear or submit at the end. */
export function ListSearch({
  value,
  onValueChange,
  onSubmit,
  placeholder,
  label,
  style
}: ListSearchProps) {
  const submit = (event?: FormEvent) => {
    event?.preventDefault()
    onSubmit(value.trim())
  }

  return (
    <form onSubmit={submit} {...stylex.props(searchStyles.form)}>
      <InputGroup style={style}>
        <InputGroupAddon>
          <Search size={16} aria-hidden />
        </InputGroupAddon>
        <InputGroupInput
          aria-label={label}
          placeholder={placeholder}
          value={value}
          onChange={(event) => onValueChange(event.target.value)}
        />
        <InputGroupAddon align='inline-end'>
          {value !== '' ? (
            <InputGroupButton
              aria-label='Clear search'
              onClick={() => {
                onValueChange('')
                onSubmit('')
              }}
            >
              Clear
            </InputGroupButton>
          ) : null}
          <InputGroupButton aria-label={label} onClick={() => submit()}>
            Search
          </InputGroupButton>
          <Kbd>↵</Kbd>
        </InputGroupAddon>
      </InputGroup>
    </form>
  )
}

const searchStyles = stylex.create({
  form: {
    display: 'flex',
    flex: 1,
    minWidth: 0
  }
})
