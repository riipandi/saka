import * as stylex from '@stylexjs/stylex'
import { format } from 'date-fns'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { FieldDescription } from 'uilibs/components/base/field'
import { Popover, PopoverContent, PopoverTrigger } from 'uilibs/components/base/popover'
import { Calendar } from 'uilibs/components/extra/calendar'
import { InputGroup, InputGroupInput } from 'uilibs/components/extra/input-group'
import { colors } from 'uilibs/styles/colors.stylex'

const styles = stylex.create({
  trigger: {
    justifyContent: 'flex-start',
    fontWeight: 'normal',
    width: '100%'
  },
  placeholder: {
    color: colors.foregroundNeutralFaded
  },
  picker: {
    display: 'flex',
    flexDirection: 'column',
    gap: 12
  }
})

const displayFormat = new Intl.DateTimeFormat('en-GB', {
  dateStyle: 'medium',
  timeStyle: 'short'
})

/**
 * The date-and-time picker the expiry fields share: the uilibs Calendar in
 * a popover for the day, a time input beside it for the wall clock. The
 * value is the wire's shape — a Date, or undefined when the field is left
 * empty (an expiry that never lifts).
 */
export function DateTimePicker({
  value,
  onChange,
  id,
  ariaLabel,
  placeholder = 'Never expires',
  description
}: {
  value: Date | undefined
  onChange: (next: Date | undefined) => void
  id?: string
  ariaLabel: string
  placeholder?: string
  description?: string
}) {
  const [open, setOpen] = useState(false)

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          render={
            <Button id={id} aria-label={ariaLabel} variant='outline' style={styles.trigger} />
          }
        >
          {value ? (
            displayFormat.format(value)
          ) : (
            <span {...stylex.props(styles.placeholder)}>{placeholder}</span>
          )}
        </PopoverTrigger>
        <PopoverContent align='start' style={styles.picker}>
          <Calendar
            mode='single'
            selected={value}
            onSelect={(next) => {
              if (next) {
                const current = value ?? new Date()
                next.setHours(current.getHours(), current.getMinutes())
                onChange(next)
              } else {
                onChange(undefined)
              }
            }}
          />
          <InputGroup>
            <InputGroupInput
              aria-label='Time'
              type='time'
              step={60}
              value={value ? format(value, 'HH:mm') : ''}
              onChange={(event) => {
                if (!value) return
                const [hours = 0, minutes = 0] = event.target.value.split(':').map(Number)
                const next = new Date(value)
                next.setHours(hours, minutes)
                onChange(next)
              }}
            />
          </InputGroup>
        </PopoverContent>
      </Popover>
      {description ? <FieldDescription>{description}</FieldDescription> : null}
    </>
  )
}
