import type { CSSProperties } from 'react'
import { Column, Hr, Row } from 'react-email'
import { Alert, CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface EmailChangeNoticeData {
  name: string
  oldEmail: string
  newEmail: string
}

interface EmailChangeNoticeProps {
  logoURL: string
  appName: string
  data: EmailChangeNoticeData
}

export const EmailChangeNotice = ({ logoURL, appName, data }: EmailChangeNoticeProps) => {
  const detailsBoxStyle = { width: '225px', padding: '12px' } satisfies CSSProperties
  const detailsLabelStyle = { margin: 0, color: '#747474' } satisfies CSSProperties
  const detailsBoxValueStyle = { margin: 0, fontSize: '14px' } satisfies CSSProperties

  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Email Change Confirmation' />
      <Hr style={{ marginTop: '16px' }} />
      <Text style={{ marginTop: '18px' }}>Hello {data.name},</Text>

      <Text>This is to confirm that your email address has been changed:</Text>

      <Row style={{ backgroundColor: '#f6f6f6', borderRadius: '12px' }}>
        <Column style={detailsBoxStyle}>
          <Text size='sm' style={detailsLabelStyle}>
            Old Email
          </Text>
          <Text style={detailsBoxValueStyle}>{data.oldEmail}</Text>
        </Column>
        <Column style={detailsBoxStyle}>
          <Text size='sm' style={detailsLabelStyle}>
            New Email
          </Text>
          <Text style={detailsBoxValueStyle}>{data.newEmail}</Text>
        </Column>
      </Row>

      <Hr style={{ marginTop: '20px' }} />

      <Alert variant='info' style={{ marginTop: '20px' }}>
        <strong>Security Notice:</strong> All your sessions have been terminated for security.
        Please sign in again with your new email address.
      </Alert>

      <Text>If you did not make this change, please contact support immediately.</Text>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because you have an account in {appName}. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

EmailChangeNotice.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}',
    oldEmail: '{{.Data.OldEmail}}',
    newEmail: '{{.Data.NewEmail}}'
  }
}

EmailChangeNotice.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'John Doe',
    oldEmail: 'old@example.com',
    newEmail: 'new@example.com'
  }
}

export default EmailChangeNotice
