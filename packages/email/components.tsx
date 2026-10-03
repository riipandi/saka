import type { CSSProperties } from 'react'
import { Column, Heading, Row } from 'react-email'
import { Button as EmailButton, Text as BaseText, Link as BaseLink } from 'react-email'

const textSizes = {
  sm: { fontSize: '14px', lineHeight: '20px' },
  md: { fontSize: '16px', lineHeight: '24px' },
  lg: { fontSize: '18px', lineHeight: '28px' }
} as const

type TextSize = keyof typeof textSizes

interface TextProps extends React.ComponentProps<typeof BaseText> {
  size?: TextSize
}

export function Text({ size = 'md', style, ...props }: TextProps) {
  return <BaseText style={{ ...textSizes[size], ...style }} {...props} />
}

interface LinkProps extends React.ComponentProps<typeof BaseLink> {
  size?: TextSize
}

export function Link({ href, style, ...props }: LinkProps) {
  const linkStyle = {
    color: '#433eaf',
    textDecoration: 'underline',
    fontFamily: 'Inter, Aptos, Arial, Helvetica, sans-serif, -apple-system'
  } satisfies CSSProperties

  return <BaseLink href={href} style={{ ...linkStyle, ...style }} {...props} />
}

interface AlertProps {
  variant: 'info' | 'warning' | 'success'
  children: React.ReactNode
  style?: CSSProperties
}

export function Alert({ variant, children, style }: AlertProps) {
  const variants = {
    info: {
      backgroundColor: '#d1ecf1',
      borderLeft: '4px solid #17a2b8'
    },
    warning: {
      backgroundColor: '#fff3cd',
      borderLeft: '4px solid #ffc107'
    },
    success: {
      backgroundColor: '#d4edda',
      borderLeft: '4px solid #28a745'
    }
  } as const

  const baseStyle = {
    padding: '18px',
    fontSize: '14px',
    lineHeight: '22px',
    margin: '0 0 16px 0',
    borderRadius: '8px'
  } satisfies CSSProperties

  return <Text style={{ ...baseStyle, ...variants[variant], ...style }}>{children}</Text>
}

interface ButtonProps {
  href: string
  children: React.ReactNode
  style?: CSSProperties
}

export function Button({ href, children, style }: ButtonProps) {
  const buttonStyle = {
    backgroundColor: 'oklch(0.1435 0.0398 265.75)',
    color: 'oklch(1 0 0)',
    padding: '12px 24px',
    borderRadius: '8px',
    fontSize: '14px',
    fontWeight: '500',
    cursor: 'pointer',
    margin: '20px auto',
    display: 'block',
    textDecoration: 'none'
  } satisfies CSSProperties

  return (
    <div style={{ textAlign: 'center' }}>
      <EmailButton style={{ ...buttonStyle, ...style }} href={href}>
        {children}
      </EmailButton>
    </div>
  )
}

export function CardHeader({ title, warning }: { title: string; warning?: boolean }) {
  const titleStyle = {
    fontSize: '22px',
    fontWeight: 'bold' as const,
    margin: 0
  } satisfies CSSProperties

  const warningStyle = {
    backgroundColor: '#ffd966',
    color: '#7f6000',
    padding: '2px 14px',
    borderRadius: '50px',
    fontSize: '13px',
    display: 'inline-block',
    margin: 0
  } satisfies CSSProperties

  return (
    <Row>
      <Column>
        <Heading as='h1' style={titleStyle}>
          {title}
        </Heading>
      </Column>
      <Column align='right'>
        {warning && (
          <Text size='sm' style={warningStyle}>
            Warning
          </Text>
        )}
      </Column>
    </Row>
  )
}

export function CardFooter({ children }: { children: React.ReactNode }) {
  return (
    <Row style={{ marginTop: '20px', borderTop: '1px solid #e5e7eb' }}>
      <Column style={{ color: '#6b7280', paddingTop: '4px' }}>{children}</Column>
    </Row>
  )
}
